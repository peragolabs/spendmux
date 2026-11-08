package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/peragolabs/spendmux/internal/providers"
)

// maxCompletionLimit rejects absurd output limits before they are priced.
const maxCompletionLimit = 1_000_000

// Defaults injected only when a spend cap governs a request that names no
// output limit. Reasoning models spend their limit on hidden reasoning too,
// so they get a larger default. Both are configurable.
func defaultCompletionLimit(model string, raw map[string]json.RawMessage) int {
	if isReasoningRequest(model, raw) {
		return envLimit("SPENDMUX_DEFAULT_REASONING_MAX_TOKENS", 32768)
	}
	return envLimit("SPENDMUX_DEFAULT_MAX_TOKENS", 4096)
}

func envLimit(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 && v <= maxCompletionLimit {
		return v
	}
	return def
}

func isReasoningRequest(model string, raw map[string]json.RawMessage) bool {
	for _, field := range []string{"reasoning_effort", "reasoning", "thinking"} {
		if v, ok := raw[field]; ok && string(v) != "null" {
			return true
		}
	}
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	for _, p := range []string{"o1", "o3", "o4"} {
		if m == p || strings.HasPrefix(m, p+"-") {
			return true
		}
	}
	if strings.HasPrefix(m, "gpt-5") && !strings.Contains(m, "chat") {
		return true
	}
	for _, s := range []string{"reasoner", "thinking", "-r1", "qwq", "magistral", "grok-3-mini", "grok-4"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// applyCompletionPolicy runs once per request after route resolution. When a
// spend cap applies it rejects multiplicity it cannot bound and marks the
// output limit as enforced, so every hop is sent with an explicit limit equal
// to what was reserved. Uncapped requests are left exactly as the client sent
// them (no injected limit, n>1 allowed).
func applyCompletionPolicy(req *openAIChatRequest, capped bool) error {
	if !capped {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(req.Raw, &raw); err != nil {
		return err
	}
	for _, field := range []string{"n", "best_of"} {
		v, ok := raw[field]
		if !ok || string(v) == "null" {
			continue
		}
		var count float64
		if json.Unmarshal(v, &count) != nil || count != 1 {
			return fmt.Errorf("%s must be 1 on a key with spend caps: extra completions multiply cost beyond the reserved bound", field)
		}
	}
	req.limitEnforced = true
	return nil
}

// completionReservation is the output-token bound for one hop: the client's
// limit, else the injected default (capped) or the 4096 estimate (uncapped).
func completionReservation(req openAIChatRequest, hop routeHop) int {
	if req.completionLimit > 0 {
		return req.completionLimit
	}
	if req.limitEnforced {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(req.Raw, &raw)
		return defaultCompletionLimit(hop.Model, raw)
	}
	return 4096
}

// hopReservation is the hard upper bound for one attempt on hop. Prompt
// tokens are bounded by request bytes (a BPE token is at least one byte).
func hopReservation(req openAIChatRequest, hop routeHop) (tokens, usd int64, priceUnknown bool) {
	completion := completionReservation(req, hop)
	tokens = int64(len(req.Raw)) + int64(completion)
	usd = providers.EstimateReservationUSDMicros(hop.Provider, hop.Model, len(req.Raw), completion)
	return tokens, usd, !providers.PriceKnown(hop.Provider, hop.Model)
}

// usageEstimateMeta bounds missing provider usage: under a cap it is the
// reserved bound, otherwise the plain ~4 chars/token estimate.
func (req openAIChatRequest) usageEstimateMeta() streamMeta {
	m := streamMeta{promptChars: len(req.Raw)}
	if req.limitEnforced {
		m.promptBound = len(req.Raw)
		m.completionBound = req.hopLimit
	}
	return m
}

// withCompletionLimit writes the enforced limit into an outgoing OpenAI-format
// body using the parameter the provider honours. OpenAI's reasoning models
// reject max_tokens, and every current OpenAI chat model accepts
// max_completion_tokens; other OpenAI-compatible providers use max_tokens.
func withCompletionLimit(body []byte, providerID, model string, req openAIChatRequest) ([]byte, error) {
	if !req.limitEnforced || req.hopLimit <= 0 {
		if providerID != "openai" || !bytes.Contains(body, []byte(`"max_tokens"`)) {
			return body, nil
		}
		// OpenAI deprecated max_tokens and its reasoning models reject it
		// (/v1/responses maps max_output_tokens onto it); every OpenAI chat
		// model accepts max_completion_tokens.
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, err
		}
		if v, ok := m["max_tokens"]; ok {
			if _, has := m["max_completion_tokens"]; !has && string(v) != "null" {
				m["max_completion_tokens"] = v
			}
			delete(m, "max_tokens")
		}
		return json.Marshal(m)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	v, _ := json.Marshal(req.hopLimit)
	_, hadMax := m["max_tokens"]
	_, hadMCT := m["max_completion_tokens"]
	switch {
	case providerID == "openai":
		delete(m, "max_tokens")
		m["max_completion_tokens"] = v
	case providerID == "azure":
		// Older Azure API versions only know max_tokens; reasoning
		// deployments only accept max_completion_tokens.
		if hadMCT || (!hadMax && isReasoningRequest(model, m)) {
			delete(m, "max_tokens")
			m["max_completion_tokens"] = v
		} else {
			m["max_tokens"] = v
		}
	default:
		m["max_tokens"] = v
		if hadMCT {
			m["max_completion_tokens"] = v
		}
	}
	return json.Marshal(m)
}

// contentCostUnbounded reports message parts whose cost the request bytes do
// not bound: remote media (an image URL is priced by its pixels, not its
// URL length) and file references. Plain text parts and inline (data: URL /
// base64) media are bounded by their bytes.
func contentCostUnbounded(messages []openAIMessage) bool {
	for _, message := range messages {
		content := strings.TrimSpace(string(message.Content))
		if !strings.HasPrefix(content, "[") {
			continue
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(message.Content, &parts) != nil {
			return true
		}
		for _, part := range parts {
			var kind string
			_ = json.Unmarshal(part["type"], &kind)
			switch kind {
			case "text", "input_text":
				continue
			case "image_url":
				var img struct {
					URL string `json:"url"`
				}
				if json.Unmarshal(part["image_url"], &img) != nil {
					var url string
					if json.Unmarshal(part["image_url"], &url) != nil {
						return true
					}
					img.URL = url
				}
				if !strings.HasPrefix(strings.TrimSpace(img.URL), "data:") {
					return true
				}
			case "input_audio":
				continue // inline base64 audio
			default:
				return true
			}
		}
	}
	return false
}

// uncertainStatus is a gateway failure that can arrive after the provider
// already processed (and billed) the request.
func uncertainStatus(status int) bool {
	return status == fiber.StatusBadGateway || status == fiber.StatusGatewayTimeout || status == 524
}

// chargeUncertainAttempt records an attempt whose outcome the proxy never saw
// at its reserved upper bound, so the caps count it immediately instead of
// silently restoring the allowance. The row is marked estimated for
// reconciliation against provider usage.
func chargeUncertainAttempt(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, providerID, model, endpoint string, req openAIChatRequest, reason string) {
	completion := req.hopLimit
	if completion <= 0 {
		completion = completionReservation(req, routeHop{Provider: providerID, Model: model})
	}
	prompt := len(req.Raw)
	if !req.limitEnforced {
		prompt = prompt/4 + 1 // no cap to protect: the usual estimate, not the hard bound
	}
	cost := providers.EstimateReservationUSDMicros(providerID, model, prompt, completion)
	logUsageWithCost(c, userID, keyID, providerID, model, endpoint, prompt, completion, prompt+completion, cost, 0, 0, 499, false,
		"upstream outcome unknown ("+truncateStr(reason, 120)+"); reserved upper bound counted against caps, reconcile with provider usage")
}
