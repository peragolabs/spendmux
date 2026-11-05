package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/actor"
	"github.com/vrannang1/spendmux/internal/providers"
	"github.com/vrannang1/spendmux/internal/telemetry"
	"github.com/vrannang1/spendmux/internal/upstream"
	"github.com/vrannang1/spendmux/models"
	"github.com/vrannang1/spendmux/oss/keys"
	"github.com/vrannang1/spendmux/oss/usage"
)

type openAIChatRequest struct {
	Provider            string          `json:"provider"`
	Model               string          `json:"model"`
	Fallback            bool            `json:"fallback"` // opt-in; missing = false
	Messages            []openAIMessage `json:"messages"`
	Stream              bool            `json:"stream"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	Raw                 json.RawMessage `json:"-"`

	// noStatusRetry: another key is waiting, so a rate-limited or failing key
	// returns at once instead of being retried (see upstream.WithoutStatusRetry).
	noStatusRetry bool
	ctx           context.Context
	// completionLimit is the client's output limit (max of max_tokens and
	// max_completion_tokens), 0 when it sent none. limitEnforced is set when a
	// spend cap governs the request: every hop then carries an explicit limit
	// (hopLimit) so the reservation is a true upper bound.
	completionLimit int
	limitEnforced   bool
	hopLimit        int
	// autoPromptCache: add prompt-cache breakpoints for providers that need
	// them (Anthropic) when the request carries none of its own.
	autoPromptCache bool
}

type openAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type openAIEmbedRequest struct {
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Input    interface{} `json:"input"`
}

type routeHop struct {
	Provider string
	Model    string
}

func authenticatePlatformKey(c *fiber.Ctx) (models.PlatformAPIKey, bool) {
	pk, err := keys.ResolvePlatformKey(c)
	switch {
	case errors.Is(err, keys.ErrKeyPaused):
		_ = c.Status(403).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "permission_error", "code": "key_paused"}})
		return models.PlatformAPIKey{}, false
	case errors.Is(err, keys.ErrKeyExpired):
		_ = c.Status(401).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "auth_error", "code": "key_expired"}})
		return models.PlatformAPIKey{}, false
	case err != nil:
		_ = c.Status(401).JSON(fiber.Map{"error": fiber.Map{"message": "invalid api key", "type": "auth_error"}})
		return models.PlatformAPIKey{}, false
	}
	// Bulkhead: cap concurrent requests per token. Released by InFlightGuard
	// for buffered responses, or by the stream writer when the stream ends.
	release, err := keys.AcquireInFlight(pk.ID)
	if err != nil {
		_ = c.Status(429).JSON(fiber.Map{"error": fiber.Map{
			"message": "too many concurrent requests on this token — wait for in-flight calls to finish",
			"type":    "rate_limit_error",
			"code":    "concurrency_limit_exceeded",
		}})
		return models.PlatformAPIKey{}, false
	}
	c.Locals("inflight_release", release)
	return pk, true
}

// InFlightGuard releases a token's bulkhead slot when a buffered /v1 response
// finishes. Streaming responses release from inside their body writer instead.
func InFlightGuard(c *fiber.Ctx) error {
	err := c.Next()
	if !c.Context().IsBodyStream() {
		releaseInFlight(c)
	}
	return err
}

func releaseInFlight(c *fiber.Ctx) {
	if r, ok := c.Locals("inflight_release").(func()); ok && r != nil {
		r()
	}
}

func rewriteChatRequest(raw []byte, model string, stream bool) ([]byte, error) {
	return rewriteChatRequestOpts(raw, model, stream, true, false)
}

// rewriteChatRequestOpts strips Spendmux fields and, for streams, asks the
// provider to append a usage chunk (stream_options.include_usage) so streamed
// calls are accounted exactly. withUsage=false is the retry for the rare
// provider that rejects the field. cache_control marks are removed unless
// keepCache (the provider accepts them).
func rewriteChatRequestOpts(raw []byte, model string, stream, withUsage, keepCache bool) ([]byte, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	delete(payload, "provider")
	delete(payload, "fallback")
	delete(payload, anthropicExtraField)
	if !keepCache {
		stripCacheControl(payload)
	}
	payload["model"] = model
	payload["stream"] = stream
	if stream {
		if withUsage {
			opts, _ := payload["stream_options"].(map[string]interface{})
			if opts == nil {
				opts = map[string]interface{}{}
			}
			opts["include_usage"] = true
			payload["stream_options"] = opts
		} else {
			delete(payload, "stream_options")
		}
	}
	return json.Marshal(payload)
}

// routeDefaultLocal names the provider a format-specific endpoint assumes
// when nothing else picks one (/v1/messages → anthropic, /v1/responses → openai).
const routeDefaultLocal = "route_default"

// routeDefault is the provider used when the request names none: the token's
// scope first, then the endpoint's default unless the model is "provider/model".
func routeDefault(c *fiber.Ctx, scope, model string) string {
	if scope != "" {
		return scope
	}
	def, _ := c.Locals(routeDefaultLocal).(string)
	if head, tail, ok := strings.Cut(model, "/"); ok && tail != "" {
		if _, known := providers.Get(strings.ToLower(head)); known {
			return ""
		}
	}
	return def
}

// V1ChatCompletions proxies OpenAI-compatible chat (and Anthropic via adapter).
func V1ChatCompletions(c *fiber.Ctx) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	userID := pk.UserID
	keyID := pk.ID
	received := time.Now()

	var req openAIChatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "invalid json body", "type": "invalid_request_error"}})
	}
	if len(req.Messages) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "messages must not be empty", "type": "invalid_request_error"}})
	}
	if req.MaxTokens < 0 || req.MaxCompletionTokens < 0 {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "max_tokens must not be negative", "type": "invalid_request_error"}})
	}
	req.Raw = append(req.Raw[:0], c.Body()...)
	if req.MaxTokens > maxCompletionLimit || req.MaxCompletionTokens > maxCompletionLimit {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": fmt.Sprintf("max_tokens must be at most %d", maxCompletionLimit), "type": "invalid_request_error"}})
	}
	req.completionLimit = max(req.MaxTokens, req.MaxCompletionTokens)
	mem, err := attachMemory(c, pk, &req)
	switch {
	case errors.Is(err, errMemoryUnavailable):
		return c.Status(503).JSON(fiber.Map{"error": fiber.Map{"message": "memory store unavailable", "type": "api_error", "code": "memory_unavailable"}})
	case err != nil:
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}
	if req.Provider == "" {
		req.Provider = strings.TrimSpace(c.Get("X-Spendmux-Provider"))
	}
	if !req.Fallback && strings.EqualFold(strings.TrimSpace(c.Get("X-Spendmux-Fallback")), "true") {
		req.Fallback = true
	}
	providerID, modelName, err := providers.ResolveRouteWithDefault(req.Provider, req.Model, routeDefault(c, pk.AllowedProvider, req.Model))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}
	if err := keys.EnforceKeyScope(pk, providerID, modelName); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "key_scope_denied"}})
	}

	c.Locals(requestHashLocal, requestFingerprint(userID, "chat.completions", providerID, modelName, req.Raw))
	req.autoPromptCache = autoPromptCache(c, pk)

	// A cache hit answers before any cap, rate limit or provider is touched.
	cache := newCacheEntry(cachePolicyFor(c, pk), userID, "chat.completions", providerID, modelName, req.Raw)
	if cache != nil {
		if hit, ok := cache.lookup(); ok {
			c.Set(cacheHeader, "hit")
			mem.save(replyFromBody(hit.body))
			latency := time.Since(received).Milliseconds()
			logCacheHit(c, userID, &keyID, hit, latency, req.Stream)
			if req.Stream {
				return replayStream(c, hit.body)
			}
			c.Set("Content-Type", "application/json")
			return c.Status(200).Send(withCacheMeta(hit.body, hit.row, latency))
		}
		c.Set(cacheHeader, "miss")
		if req.Stream {
			c.Locals(streamRecorderLocal, newStreamRecorder(cache))
		}
	}

	hops := []routeHop{{Provider: providerID, Model: modelName}}
	if req.Fallback {
		steps, err := keys.LoadFallbackSteps(userID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fiber.Map{"message": "failed to load fallback chain", "type": "api_error"}})
		}
		if len(steps) == 0 {
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{
				"message": "fallback is true but no fallback chain is configured — add one at /fallback",
				"type":    "invalid_request_error",
				"code":    "fallback_not_configured",
			}})
		}
		for _, s := range steps {
			p, m, err := providers.ResolveRoute(s.Provider, s.Model)
			if err != nil {
				continue
			}
			if err := keys.EnforceKeyScope(pk, p, m); err != nil {
				continue
			}
			if p == providerID && m == modelName {
				continue
			}
			hops = append(hops, routeHop{Provider: p, Model: m})
		}
		if len(hops) == 1 {
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{
				"message": "fallback chain has no usable hops beyond the primary — check /fallback",
				"type":    "invalid_request_error",
				"code":    "fallback_chain_empty",
			}})
		}
	}

	sessionID, tenantID := spendHeaders(c)
	capped := keys.SpendCapsApply(pk, sessionID, tenantID)
	if err := applyCompletionPolicy(&req, capped); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "spend_cap_requires_bounded_request"}})
	}
	contentUnknown := contentCostUnbounded(req.Messages)
	// The first attempt reserves the primary hop only. Each later attempt
	// (another key, or a fallback hop) grows the reservation to that hop's
	// bound before it is sent; see growForAttempt.
	tokens, usd, priceUnknown := hopReservation(req, hops[0])
	priceUnknown = priceUnknown || contentUnknown
	spend, err := keys.EnforceSpendControls(keys.SpendRequest{
		Key:          pk,
		Tokens:       tokens,
		USDMicros:    usd,
		SessionID:    sessionID,
		TenantID:     tenantID,
		LoopHash:     keys.HashLoopRequest(modelName, req.Messages),
		PriceUnknown: priceUnknown,
		RateTokens:   int64((len(req.Raw)+3)/4 + completionReservation(req, hops[0])),
	})
	if err != nil {
		return writeSpendError(c, err)
	}
	if spend.SessionWarn {
		c.Set("X-Spendmux-Session-Warn", "80")
	}
	if req.limitEnforced {
		// Under a cap, missing provider usage is charged at the reserved bound.
		c.Locals("prompt_reservation", len(req.Raw))
	}
	reservationID := spend.ReservationID
	persistenceFailed := &atomic.Bool{}
	c.Locals("reservation_id", reservationID)
	c.Locals("usage_persistence_failed", persistenceFailed)
	requestContext, cancelRequest := context.WithTimeout(c.UserContext(), 30*time.Minute)
	req.ctx = requestContext
	reservationHandedToStream := false
	defer func() {
		if !reservationHandedToStream {
			cancelRequest()
			releaseSettledReservation(reservationID, persistenceFailed)
		}
	}()

	var attempts []map[string]interface{}
	start := time.Now()
	firstAttempt := true
	// growForAttempt makes sure the reservation covers the next paid attempt
	// (plus any usage already recorded for earlier attempts) before it is sent.
	growForAttempt := func(hop routeHop) error {
		if firstAttempt {
			firstAttempt = false
			return nil
		}
		t, u, unknown := hopReservation(req, hop)
		return keys.GrowReservation(reservationID, keys.SpendRequest{
			Key: pk, Tokens: t, USDMicros: u, SessionID: sessionID, TenantID: tenantID,
			PriceUnknown: unknown || contentUnknown,
		})
	}

	for i, hop := range hops {
		if requestContext.Err() != nil {
			return c.Status(504).JSON(fiber.Map{"error": "upstream request deadline exceeded"})
		}
		req.hopLimit = completionReservation(req, hop)
		if req.limitEnforced {
			c.Locals("completion_reservation", req.hopLimit)
		}
		if req.limitEnforced {
			req.MaxTokens = req.hopLimit // the Anthropic translation reads MaxTokens
		}
		cfg, ok := providers.Get(hop.Provider)
		if !ok {
			attempts = append(attempts, map[string]interface{}{
				"provider": hop.Provider, "model": hop.Model, "status": 400, "error": "unknown provider",
			})
			continue
		}
		cands, err := loadKeyCandidates(userID, cfg)
		if err != nil {
			attempts = append(attempts, map[string]interface{}{
				"provider": hop.Provider, "model": hop.Model, "status": 400, "error": err.Error(),
			})
			if req.Fallback && i < len(hops)-1 {
				continue
			}
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}, "meta": fiber.Map{"attempts": attempts}})
		}

		// Walk this provider's keys: a rate-limited or failing key cools down
		// and the next one is tried after a short jittered backoff.
		var status int
		var body []byte
		var openErr error
		opened := false
		skipHop := false
		tries := maxKeyAttempts(len(cands))
		for j := 0; j < tries; j++ {
			cand := cands[j]
			// Same-key status retries only resend 429/503 replies, which the
			// provider did not bill; they stay on for the last candidate key.
			req.noStatusRetry = j < tries-1
			hcfg, apiKey, err := cand.open(cfg)
			if err != nil {
				openErr = err
				attempts = append(attempts, map[string]interface{}{
					"provider": hop.Provider, "model": hop.Model, "key": cand.row.KeyHint, "status": 400, "error": err.Error(),
				})
				continue
			}
			opened = true
			if err := growForAttempt(hop); err != nil {
				if i > 0 && errors.Is(err, keys.ErrPriceUnknown) {
					// A fallback hop the USD cap cannot price is skipped.
					attempts = append(attempts, map[string]interface{}{
						"provider": hop.Provider, "model": hop.Model, "status": 400, "error": err.Error(),
					})
					skipHop = true
					break
				}
				// The caps cannot cover another paid attempt: stop here.
				return writeSpendError(c, err)
			}

			// Streaming: peek status before committing SSE so we can walk keys and the ladder.
			if req.Stream {
				st, resp, hopErr, mayBill := beginUpstreamStream(hcfg, apiKey, hop.Model, req)
				att := map[string]interface{}{"provider": hop.Provider, "model": hop.Model, "key": cand.row.KeyHint, "status": st}
				if hopErr != "" {
					att["error"] = hopErr
				}
				attempts = append(attempts, att)
				if st >= 200 && st < 300 && resp != nil {
					cand.succeeded()
					reservationHandedToStream = true
					resp.Body = &requestBody{ReadCloser: resp.Body, cancel: cancelRequest}
					if hcfg.NativeAnthropic {
						return streamAnthropicAsOpenAI(c, userID, &keyID, hcfg.ID, hop.Model, resp, start, reservationID)
					}
					return streamOpenAIChat(c, userID, &keyID, hcfg.ID, hop.Model, resp, start, reservationID)
				}
				retryAfter := ""
				if resp != nil {
					retryAfter = resp.Header.Get("Retry-After")
					_ = resp.Body.Close()
				}
				if mayBill || (resp != nil && uncertainStatus(st)) {
					reason := hopErr
					if reason == "" {
						reason = fmt.Sprintf("upstream gateway status %d", st)
					}
					chargeUncertainAttempt(c, userID, &keyID, hcfg.ID, hop.Model, "chat.completions", req, reason)
				}
				status, body = st, nil
				if cand.failed(st, retryAfter, hopErr) && j < tries-1 {
					keyBackoff(j)
					continue
				}
				break
			}

			var meta map[string]interface{}
			var hopErr string
			if hcfg.NativeAnthropic {
				status, body, meta, hopErr = executeAnthropicChat(c, userID, &keyID, hcfg, apiKey, hop.Model, req, start)
			} else {
				status, body, meta, hopErr = executeOpenAIChat(c, userID, &keyID, hcfg, apiKey, hop.Model, req, start)
			}
			att := map[string]interface{}{
				"provider":   hop.Provider,
				"model":      hop.Model,
				"key":        cand.row.KeyHint,
				"status":     status,
				"latency_ms": meta["latency_ms"],
			}
			if hopErr != "" {
				att["error"] = hopErr
			}
			attempts = append(attempts, att)

			if status >= 200 && status < 300 {
				cand.succeeded()
				mem.save(replyFromBody(body))
				if cache != nil {
					p, ct, _, _, _, _, _ := parseOpenAIUsage(body)
					cache.store(hop.Provider, hop.Model, body, p, ct)
				}
				var envelope map[string]interface{}
				if json.Unmarshal(body, &envelope) == nil {
					if m, ok := envelope["meta"].(map[string]interface{}); ok {
						if req.Fallback || len(attempts) > 1 {
							m["attempts"] = attempts
						}
						if req.Fallback {
							m["fallback"] = true
						}
						envelope["meta"] = m
					} else if req.Fallback {
						envelope["meta"] = map[string]interface{}{
							"provider":   hop.Provider,
							"fallback":   true,
							"attempts":   attempts,
							"latency_ms": meta["latency_ms"],
							"cost_usd":   meta["cost_usd"],
						}
					}
					out, _ := json.Marshal(envelope)
					c.Set("Content-Type", "application/json")
					return c.Status(status).Send(out)
				}
				c.Set("Content-Type", "application/json")
				return c.Status(status).Send(body)
			}
			retryAfter, _ := meta["retry_after"].(string)
			reason := hopErr
			if reason == "" {
				reason = truncateStr(string(body), 160)
			}
			if cand.failed(status, retryAfter, reason) && j < tries-1 {
				keyBackoff(j)
				continue
			}
			break
		}

		if skipHop {
			continue
		}
		if !opened { // every key failed to decrypt or had a bad endpoint
			if req.Fallback && i < len(hops)-1 {
				continue
			}
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": openErr.Error(), "type": "invalid_request_error"}, "meta": fiber.Map{"attempts": attempts}})
		}

		if req.Stream {
			if !req.Fallback || !isRetryableStatus(status) || i == len(hops)-1 {
				if req.Fallback {
					return c.Status(status).JSON(fiber.Map{
						"error": fiber.Map{"message": "upstream error", "type": "api_error"},
						"meta":  fiber.Map{"fallback": true, "attempts": attempts},
					})
				}
				return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"message": "upstream error", "type": "api_error"}})
			}
			continue
		}

		if !req.Fallback || !isRetryableStatus(status) || i == len(hops)-1 {
			c.Set("Content-Type", "application/json")
			if req.Fallback {
				var envelope map[string]interface{}
				if json.Unmarshal(body, &envelope) != nil {
					envelope = map[string]interface{}{"error": fiber.Map{"message": "upstream error", "type": "api_error"}}
				}
				envelope["meta"] = map[string]interface{}{"fallback": true, "attempts": attempts}
				out, _ := json.Marshal(envelope)
				return c.Status(status).Send(out)
			}
			return c.Status(status).Send(body)
		}
	}

	return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "all fallback hops failed", "type": "api_error"}, "meta": fiber.Map{"attempts": attempts}})
}

// beginUpstreamStream opens a streaming request. mayBill reports a transport
// failure after which the provider may still have processed the request.
func beginUpstreamStream(cfg providers.Config, apiKey, modelName string, req openAIChatRequest) (status int, resp *http.Response, errMsg string, mayBill bool) {
	if cfg.NativeAnthropic {
		body, err := anthropicRequest(req, modelName, true)
		if err != nil {
			return 400, nil, err.Error(), false
		}
		url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath
		httpReq, _ := http.NewRequestWithContext(chatContext(req), "POST", url, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		r, err := upstream.DoStream(keyAttempt(httpReq, req.noStatusRetry))
		if err != nil {
			return 502, nil, err.Error(), upstream.MayHaveReachedProvider(err)
		}
		return r.StatusCode, r, "", false
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath
	send := func(withUsage bool) (int, *http.Response, string, bool) {
		body, err := rewriteChatRequestOpts(req.Raw, modelName, true, withUsage, keepsCacheControl(cfg.ID))
		if err == nil {
			body, err = withCompletionLimit(body, cfg.ID, modelName, req)
		}
		if err != nil {
			return 400, nil, err.Error(), false
		}
		httpReq, _ := http.NewRequestWithContext(chatContext(req), "POST", url, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		if err := setUpstreamAuth(httpReq, cfg, apiKey, body); err != nil {
			return 400, nil, err.Error(), false
		}
		r, err := upstream.DoStream(keyAttempt(httpReq, req.noStatusRetry))
		if err != nil {
			return 502, nil, err.Error(), upstream.MayHaveReachedProvider(err)
		}
		return r.StatusCode, r, "", false
	}
	status, r, errMsg, mayBill := send(true)
	if status == 400 && r != nil {
		// A provider that doesn't know stream_options: retry once without it.
		peek, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		_ = r.Body.Close()
		if bytes.Contains(bytes.ToLower(peek), []byte("stream_options")) {
			return send(false)
		}
		r.Body = io.NopCloser(bytes.NewReader(peek))
	}
	return status, r, errMsg, mayBill
}

func isRetryableStatus(status int) bool {
	return status == 429 || status == 502 || status == 503 || status == 504
}

// V1Embeddings proxies OpenAI-compatible embeddings.
func V1Embeddings(c *fiber.Ctx) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	userID := pk.UserID
	keyID := pk.ID
	received := time.Now()

	var req openAIEmbedRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "invalid json body", "type": "invalid_request_error"}})
	}
	inputBytes, _ := json.Marshal(req.Input)
	providerID, modelName, err := providers.ResolveRouteWithDefault(req.Provider, req.Model, pk.AllowedProvider)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}
	if err := keys.EnforceKeyScope(pk, providerID, modelName); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "key_scope_denied"}})
	}
	cache := newCacheEntry(embeddingsCachePolicy(c, pk), userID, "embeddings", providerID, modelName, c.Body())
	if hit, ok := cache.lookup(); ok {
		c.Set(cacheHeader, "hit")
		latency := time.Since(received).Milliseconds()
		logCacheHit(c, userID, &keyID, hit, latency, false)
		c.Set("Content-Type", "application/json")
		return c.Status(200).Send(withCacheMeta(hit.body, hit.row, latency))
	} else if cache != nil {
		c.Set(cacheHeader, "miss")
	}
	// Input bytes bound the tokens of one attempt; a later key attempt
	// re-checks the caps first (GrowReservation below).
	inputBound := len(inputBytes)
	tokens := int64(inputBound)
	usd := providers.EstimateCostUSDMicros(providerID, modelName, inputBound, 0)
	sessionID, tenantID := spendHeaders(c)
	embedSpend := keys.SpendRequest{
		Key:          pk,
		Tokens:       tokens,
		USDMicros:    usd,
		SessionID:    sessionID,
		TenantID:     tenantID,
		PriceUnknown: !providers.PriceKnown(providerID, modelName),
		RateTokens:   int64((inputBound + 3) / 4),
	}
	spend, err := keys.EnforceSpendControls(embedSpend)
	if err != nil {
		return writeSpendError(c, err)
	}
	if spend.SessionWarn {
		c.Set("X-Spendmux-Session-Warn", "80")
	}
	persistenceFailed := &atomic.Bool{}
	c.Locals("reservation_id", spend.ReservationID)
	c.Locals("usage_persistence_failed", persistenceFailed)
	defer releaseSettledReservation(spend.ReservationID, persistenceFailed)
	requestContext, cancelRequest := context.WithTimeout(c.UserContext(), 30*time.Minute)
	defer cancelRequest()

	cfg, ok := providers.Get(providerID)
	if !ok {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "unknown provider: " + providerID, "type": "invalid_request_error"}})
	}
	if cfg.EmbedPath == "" {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "provider does not support embeddings", "type": "invalid_request_error"}})
	}

	cands, err := loadKeyCandidates(userID, cfg)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}

	payload := map[string]interface{}{
		"model": modelName,
		"input": req.Input,
	}
	body, _ := json.Marshal(payload)

	start := time.Now()
	var resp *http.Response
	var respBody []byte
	tries := maxKeyAttempts(len(cands))
	for j := 0; j < tries; j++ {
		cand := cands[j]
		hcfg, apiKey, err := cand.open(cfg)
		if err != nil {
			if j < tries-1 {
				continue
			}
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
		}
		httpReq, _ := http.NewRequestWithContext(requestContext, "POST", strings.TrimRight(hcfg.BaseURL, "/")+hcfg.EmbedPath, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		if err := setUpstreamAuth(httpReq, hcfg, apiKey, body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
		}
		if j > 0 {
			if err := keys.GrowReservation(spend.ReservationID, embedSpend); err != nil {
				return writeSpendError(c, err)
			}
		}
		chargeUnknown := func(reason string) {
			logUsageWithCost(c, userID, &keyID, providerID, modelName, "embeddings", inputBound, 0, inputBound, usd, time.Since(start).Milliseconds(), 0, 499, false,
				"upstream outcome unknown ("+truncateStr(reason, 120)+"); reserved upper bound counted against caps, reconcile with provider usage")
		}
		r, err := upstream.Do(keyAttempt(httpReq, j < tries-1))
		if err != nil {
			if upstream.MayHaveReachedProvider(err) {
				chargeUnknown(err.Error())
			} else {
				logUsage(c, userID, &keyID, providerID, modelName, "embeddings", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
			}
			if cand.failed(0, "", err.Error()) && j < tries-1 {
				keyBackoff(j)
				continue
			}
			return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream unavailable", "type": "api_error"}})
		}
		rb, err := upstream.ReadAllLimit(r.Body, 8<<20)
		_ = r.Body.Close()
		if err != nil {
			if r.StatusCode < 300 || uncertainStatus(r.StatusCode) {
				chargeUnknown(err.Error())
			} else {
				logUsage(c, userID, &keyID, providerID, modelName, "embeddings", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
			}
			return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream response too large or unreadable", "type": "api_error"}})
		}
		resp, respBody = r, rb
		if uncertainStatus(r.StatusCode) {
			chargeUnknown(fmt.Sprintf("upstream gateway status %d", r.StatusCode))
		}
		if r.StatusCode >= 200 && r.StatusCode < 300 {
			cand.succeeded()
			break
		}
		if cand.failed(r.StatusCode, r.Header.Get("Retry-After"), truncateStr(string(rb), 200)) && j < tries-1 {
			logUsage(c, userID, &keyID, providerID, modelName, "embeddings", 0, 0, 0, time.Since(start).Milliseconds(), 0, r.StatusCode, false, truncateStr(string(rb), 200))
			keyBackoff(j)
			continue
		}
		break
	}
	latency := time.Since(start).Milliseconds()

	var usage struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(respBody, &usage)
	promptTok := usage.Usage.PromptTokens
	if promptTok == 0 {
		promptTok = usage.Usage.TotalTokens
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && promptTok <= 0 {
		promptTok = inputBound
	}
	logUsage(c, userID, &keyID, providerID, modelName, "embeddings", promptTok, 0, promptTok, latency, 0, resp.StatusCode, false, "")
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		cache.store(providerID, modelName, respBody, promptTok, 0)
	}

	c.Set("Content-Type", "application/json")
	return c.Status(resp.StatusCode).Send(respBody)
}

func writeBudgetError(c *fiber.Ctx, err error) error {
	return writeSpendError(c, err)
}

func writeSpendError(c *fiber.Ctx, err error) error {
	code := "token_budget_exceeded"
	message := err.Error()
	switch {
	case errors.Is(err, keys.ErrPlatformKeyRateLimit):
		c.Set("Retry-After", strconv.Itoa(keys.APIKeyRateLimitRetryAfter(err)))
		code = "rate_limit_exceeded"
		message = "rate limit exceeded"
	case errors.Is(err, keys.ErrTPMLimit):
		code = "tpm_exceeded"
	case errors.Is(err, keys.ErrLoopDetected):
		code = "loop_detected"
	case errors.Is(err, keys.ErrSessionBudget):
		code = "session_budget_exceeded"
	case errors.Is(err, keys.ErrTenantBudget):
		code = "tenant_budget_exceeded"
	case errors.Is(err, keys.ErrDailyUSD), errors.Is(err, keys.ErrWeeklyUSD), errors.Is(err, keys.ErrMonthlyUSD):
		code = "usd_budget_exceeded"
	case errors.Is(err, keys.ErrDailyBudget), errors.Is(err, keys.ErrWeeklyBudget):
		code = "token_budget_exceeded"
	case errors.Is(err, keys.ErrReservationLost):
		code = "reservation_expired"
	case errors.Is(err, keys.ErrPriceUnknown):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fiber.Map{
			"message": message + ". Use a model with a published price, or a token without a USD cap.",
			"type":    "invalid_request_error", "code": "model_price_unknown",
		}})
	default:
		slog.Error("spend_control_failed", "error", err)
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": fiber.Map{
			"message": "spend control service unavailable", "type": "api_error",
		}})
	}
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{
		"message": message, "type": "rate_limit_error", "code": code,
	}})
}

func spendHeaders(c *fiber.Ctx) (session, tenant string) {
	return strings.TrimSpace(c.Get("X-Spendmux-Session")), strings.TrimSpace(c.Get("X-Spendmux-Tenant"))
}

// ResolveAPIKey loads one of the user's keys for cfg (weighted, skipping keys
// that are cooling down) and applies derived / override base URLs.
// For aws-sigv4 / vertex the secret is encrypted JSON (see providers.AWSCreds / VertexCreds).
func ResolveAPIKey(userID uuid.UUID, cfg *providers.Config) (apiKey string, err error) {
	cands, err := loadKeyCandidates(userID, *cfg)
	if err != nil {
		return "", err
	}
	for _, k := range cands {
		var opened providers.Config
		opened, apiKey, err = k.open(*cfg)
		if err == nil {
			*cfg = opened
			return apiKey, nil
		}
	}
	return "", err
}

// SetUpstreamAuth applies the provider's auth style to an outbound request.
func SetUpstreamAuth(req *http.Request, cfg providers.Config, apiKey string, body []byte) error {
	return setUpstreamAuth(req, cfg, apiKey, body)
}

func setUpstreamAuth(req *http.Request, cfg providers.Config, secret string, body []byte) error {
	switch cfg.AuthStyle {
	case "x-api-key":
		req.Header.Set("x-api-key", secret)
		req.Header.Set("anthropic-version", "2023-06-01")
		return nil
	case "api-key":
		req.Header.Set("api-key", secret)
		return nil
	case "aws-sigv4":
		creds, err := providers.ParseAWSCreds(secret)
		if err != nil {
			return err
		}
		return providers.SignAWSRequestV4(req, creds, body)
	case "vertex":
		creds, err := providers.ParseVertexCreds(secret)
		if err != nil {
			return err
		}
		tok, err := providers.VertexAccessToken(creds.ServiceAccountJSON)
		if err != nil {
			return fmt.Errorf("vertex token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		return nil
	default:
		req.Header.Set("Authorization", "Bearer "+secret)
		return nil
	}
}

func executeOpenAIChat(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, cfg providers.Config, apiKey, modelName string, req openAIChatRequest, start time.Time) (status int, body []byte, meta map[string]interface{}, errMsg string) {
	meta = map[string]interface{}{}
	reqBody, err := rewriteChatRequestOpts(req.Raw, modelName, false, true, keepsCacheControl(cfg.ID))
	if err == nil {
		reqBody, err = withCompletionLimit(reqBody, cfg.ID, modelName, req)
	}
	if err != nil {
		return 400, []byte(`{"error":{"message":"invalid json body","type":"invalid_request_error"}}`), meta, err.Error()
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath

	httpReq, _ := http.NewRequestWithContext(chatContext(req), "POST", url, bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	if err := setUpstreamAuth(httpReq, cfg, apiKey, reqBody); err != nil {
		meta["latency_ms"] = time.Since(start).Milliseconds()
		return 400, []byte(`{"error":{"message":"auth config error","type":"invalid_request_error"}}`), meta, err.Error()
	}

	resp, err := upstream.Do(keyAttempt(httpReq, req.noStatusRetry))
	if err != nil {
		if upstream.MayHaveReachedProvider(err) {
			chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, err.Error())
		} else {
			logUsage(c, userID, keyID, cfg.ID, modelName, "chat.completions", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		meta["latency_ms"] = time.Since(start).Milliseconds()
		return 502, []byte(`{"error":{"message":"upstream unavailable","type":"api_error"}}`), meta, err.Error()
	}
	defer resp.Body.Close()

	respBody, err := upstream.ReadAllLimit(resp.Body, 8<<20)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		if resp.StatusCode < 300 || uncertainStatus(resp.StatusCode) {
			// The provider answered (and billed) but the reply was lost.
			chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, err.Error())
		} else {
			logUsage(c, userID, keyID, cfg.ID, modelName, "chat.completions", 0, 0, 0, latency, 0, 502, false, err.Error())
		}
		meta["latency_ms"] = latency
		return 502, []byte(`{"error":{"message":"upstream response too large or unreadable","type":"api_error"}}`), meta, err.Error()
	}
	latency := time.Since(start).Milliseconds()
	if uncertainStatus(resp.StatusCode) {
		chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, fmt.Sprintf("upstream gateway status %d", resp.StatusCode))
		meta["latency_ms"] = latency
		return resp.StatusCode, respBody, meta, ""
	}
	promptTok, completionTok, totalTok, cachedTok, reasoningTok, _, _ := parseOpenAIUsage(respBody)
	note := ""
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var estimated bool
		promptTok, completionTok, estimated = estimateIfMissing(req.usageEstimateMeta(), promptTok, completionTok, 0)
		if estimated {
			totalTok = promptTok + completionTok
			note = "provider reported incomplete usage; reserved upper bound charged against caps"
			meta["usage_estimated"] = true
		}
	}
	pc := cacheTokens{read: cachedTok, write: cacheWriteTokens(respBody)}
	costMicros := providers.EstimateCachedCostUSDMicros(cfg.ID, modelName, promptTok, completionTok, pc.read, pc.write)
	logUsageFull(c, userID, keyID, cfg.ID, modelName, "chat.completions", promptTok, completionTok, totalTok, costMicros, latency, 0, resp.StatusCode, false, note, pc, reasoningTok)

	if ra := resp.Header.Get("Retry-After"); ra != "" && resp.StatusCode != 200 {
		meta["retry_after"] = ra
	}
	meta["provider"] = cfg.ID
	meta["latency_ms"] = latency
	meta["cost_usd"] = float64(costMicros) / 1e6
	meta["cost_source"] = "list"

	if resp.StatusCode == 200 {
		var envelope map[string]interface{}
		if json.Unmarshal(respBody, &envelope) == nil {
			if up, ok := envelope["provider"].(string); ok && up != "" && !strings.EqualFold(up, cfg.ID) {
				meta["upstream_provider"] = up
			}
			envelope["meta"] = meta
			out, _ := json.Marshal(envelope)
			return 200, out, meta, ""
		}
	}
	return resp.StatusCode, respBody, meta, ""
}

func executeAnthropicChat(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, cfg providers.Config, apiKey, modelName string, req openAIChatRequest, start time.Time) (status int, body []byte, meta map[string]interface{}, errMsg string) {
	meta = map[string]interface{}{}
	reqBody, err := anthropicRequest(req, modelName, false)
	if err != nil {
		return 400, []byte(`{"error":{"message":"invalid Anthropic-compatible request","type":"invalid_request_error"}}`), meta, err.Error()
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath
	httpReq, _ := http.NewRequestWithContext(chatContext(req), "POST", url, bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := upstream.Do(keyAttempt(httpReq, req.noStatusRetry))
	if err != nil {
		if upstream.MayHaveReachedProvider(err) {
			chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, err.Error())
		} else {
			logUsage(c, userID, keyID, cfg.ID, modelName, "chat.completions", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		meta["latency_ms"] = time.Since(start).Milliseconds()
		return 502, []byte(`{"error":{"message":"upstream unavailable","type":"api_error"}}`), meta, err.Error()
	}
	defer resp.Body.Close()
	respBody, err := upstream.ReadAllLimit(resp.Body, 8<<20)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		if resp.StatusCode < 300 || uncertainStatus(resp.StatusCode) {
			// The provider answered (and billed) but the reply was lost.
			chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, err.Error())
		} else {
			logUsage(c, userID, keyID, cfg.ID, modelName, "chat.completions", 0, 0, 0, latency, 0, 502, false, err.Error())
		}
		meta["latency_ms"] = latency
		return 502, []byte(`{"error":{"message":"upstream response too large or unreadable","type":"api_error"}}`), meta, err.Error()
	}
	latency := time.Since(start).Milliseconds()
	if uncertainStatus(resp.StatusCode) {
		chargeUncertainAttempt(c, userID, keyID, cfg.ID, modelName, "chat.completions", req, fmt.Sprintf("upstream gateway status %d", resp.StatusCode))
		meta["latency_ms"] = latency
		return resp.StatusCode, respBody, meta, ""
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" && resp.StatusCode != 200 {
		meta["retry_after"] = ra
	}
	meta["latency_ms"] = latency
	meta["provider"] = cfg.ID
	if resp.StatusCode != 200 {
		logUsage(c, userID, keyID, cfg.ID, modelName, "chat.completions", 0, 0, 0, latency, 0, resp.StatusCode, false, truncateStr(string(respBody), 200))
		return resp.StatusCode, respBody, meta, ""
	}

	var anth anthropicMessage
	if err := json.Unmarshal(respBody, &anth); err != nil {
		return 502, []byte(`{"error":{"message":"bad anthropic response","type":"api_error"}}`), meta, err.Error()
	}
	promptTok := anth.Usage.prompt()
	completionTok := anth.Usage.OutputTokens
	var estimated bool
	promptTok, completionTok, estimated = estimateIfMissing(req.usageEstimateMeta(), promptTok, completionTok, 0)
	note := ""
	if estimated {
		note = "provider reported incomplete usage; reserved upper bound charged against caps"
		meta["usage_estimated"] = true
	}
	pc := anth.Usage.cache()
	cost := providers.EstimateCachedCostUSDMicros(cfg.ID, modelName, promptTok, completionTok, pc.read, pc.write)
	logUsageFull(c, userID, keyID, cfg.ID, modelName, "chat.completions", promptTok, completionTok, promptTok+completionTok, cost, latency, 0, 200, false, note, pc, 0)
	meta["cost_usd"] = float64(cost) / 1e6

	msg, finish := chatMessageFromAnthropic(anth)
	out := map[string]interface{}{
		"id":      anth.ID,
		"object":  "chat.completion",
		"model":   modelName,
		"choices": []map[string]interface{}{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage": map[string]interface{}{
			"prompt_tokens": promptTok, "completion_tokens": completionTok, "total_tokens": promptTok + completionTok,
			"prompt_tokens_details": anth.Usage.details(),
			"cost_usd":              float64(cost) / 1e6,
		},
		"meta": meta,
	}
	raw, _ := json.Marshal(out)
	return 200, raw, meta, ""
}

// streamMeta is everything a stream writer needs from the request. It is
// captured before the handler returns because Fiber recycles *fiber.Ctx after
// that, while SetBodyStreamWriter runs later — touching c there panics.
type streamMeta struct {
	usage           usageCtx
	release         func()
	completionBound int
	promptBound     int
	promptChars     int             // request body size, for a token estimate when the provider reports none
	mem             *memorySession  // set when the request opted into memory
	out             sseWriter       // client wire format; nil = OpenAI chat chunks as-is
	rec             *streamRecorder // set when the reply should be cached
}

// emit writes one OpenAI-format SSE line in the client's wire format.
func (m streamMeta) emit(w *bufio.Writer, line string) {
	m.rec.observe(line)
	if m.out == nil {
		_, _ = w.WriteString(line)
		_ = w.WriteByte('\n')
		return
	}
	m.out.Line(w, line)
}

func captureStreamMeta(c *fiber.Ctx) streamMeta {
	r, _ := c.Locals("inflight_release").(func())
	mem, _ := c.Locals("memory").(*memorySession)
	out, _ := c.Locals(sseWriterLocal).(sseWriter)
	rec, _ := c.Locals(streamRecorderLocal).(*streamRecorder)
	completionBound, _ := c.Locals("completion_reservation").(int)
	promptBound, _ := c.Locals("prompt_reservation").(int)
	return streamMeta{completionBound: completionBound, promptBound: promptBound, usage: usageCtxFrom(c), release: r, promptChars: len(c.Body()), mem: mem, out: out, rec: rec}
}

// estimateIfMissing fills token counts a provider did not report (~4 chars/token).
func estimateIfMissing(meta streamMeta, promptTok, completionTok, streamedChars int) (int, int, bool) {
	estimated := false
	if promptTok == 0 && meta.promptChars > 0 {
		promptTok = meta.promptChars/4 + 1
		if meta.promptBound > 0 {
			promptTok = meta.promptBound
		}
		estimated = true
	}
	if completionTok == 0 && (streamedChars > 0 || meta.completionBound > 0) {
		completionTok = streamedChars/4 + 1
		if meta.completionBound > 0 {
			completionTok = meta.completionBound
		}
		estimated = true
	}
	return promptTok, completionTok, estimated
}

func streamOpenAIChat(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, providerID, modelName string, resp *http.Response, start time.Time, reservationID uuid.UUID) error {
	meta := captureStreamMeta(c)
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	var ttft int64
	var promptTok, completionTok int
	var pc cacheTokens
	var streamedChars int // completion text relayed so far; used to estimate tokens if the client aborts
	var reply strings.Builder
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer func() {
			if meta.release != nil {
				meta.release()
			}
		}()
		defer resp.Body.Close() // closing the body tears down the upstream connection → provider stops generating
		defer releaseSettledReservation(reservationID, meta.usage.failed)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		completed := false
		for scanner.Scan() {
			line := scanner.Text()
			if ttft == 0 && strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
				ttft = time.Since(start).Milliseconds()
			}

			// Capture usage from final chunk when present
			if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
					Usage *struct {
						PromptTokens        int `json:"prompt_tokens"`
						CompletionTokens    int `json:"completion_tokens"`
						TotalTokens         int `json:"total_tokens"`
						PromptTokensDetails struct {
							CachedTokens     int `json:"cached_tokens"`
							CacheWriteTokens int `json:"cache_write_tokens"`
						} `json:"prompt_tokens_details"`
					} `json:"usage"`
				}
				_ = json.Unmarshal([]byte(line[6:]), &chunk)
				for _, ch := range chunk.Choices {
					streamedChars += len(ch.Delta.Content)
					if meta.mem != nil {
						reply.WriteString(ch.Delta.Content)
					}
				}
				if chunk.Usage != nil {
					promptTok = chunk.Usage.PromptTokens
					completionTok = chunk.Usage.CompletionTokens
					pc = cacheTokens{read: chunk.Usage.PromptTokensDetails.CachedTokens, write: chunk.Usage.PromptTokensDetails.CacheWriteTokens}
				}
			}
			meta.emit(w, line)
			if err := w.Flush(); err != nil {
				// Client went away mid-stream. The provider still bills for what it
				// generated, so record an estimate rather than nothing.
				logClientAbort(meta, userID, keyID, providerID, modelName, promptTok, completionTok, streamedChars, start, ttft, pc)
				return
			}
			if line == "data: [DONE]" {
				completed = true
				break
			}
		}
		latency := time.Since(start).Milliseconds()
		if !completed {
			errMsg := "upstream stream ended before completion"
			if err := scanner.Err(); err != nil {
				errMsg = "upstream stream error: " + truncateStr(err.Error(), 300)
			}
			// The provider bills for what it generated before the cut, so count
			// an estimate against the caps under the upstream's own status.
			promptTok, completionTok, _ = estimateIfMissing(meta, promptTok, completionTok, streamedChars)
			logUsageStream(meta, userID, keyID, providerID, modelName, "chat.completions", promptTok, completionTok, promptTok+completionTok, latency, ttft, resp.StatusCode, true, errMsg+"; usage estimated", pc)
			return
		}
		meta.mem.save(replyFromText(reply.String()))
		note := ""
		if p, cTok, est := estimateIfMissing(meta, promptTok, completionTok, streamedChars); est {
			promptTok, completionTok, note = p, cTok, "provider reported no usage; tokens estimated"
		}
		meta.rec.finishOK(providerID, modelName, promptTok, completionTok)
		logUsageStream(meta, userID, keyID, providerID, modelName, "chat.completions", promptTok, completionTok, promptTok+completionTok, latency, ttft, resp.StatusCode, true, note, pc)
	})
	return nil
}

func streamAnthropicAsOpenAI(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, providerID, modelName string, resp *http.Response, start time.Time, reservationID uuid.UUID) error {
	meta := captureStreamMeta(c)
	var streamedChars int
	var reply strings.Builder // assistant text, kept only when the request opted into memory
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	var ttft int64
	var promptTok, completionTok int
	var pc cacheTokens

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer func() {
			if meta.release != nil {
				meta.release()
			}
		}()
		defer resp.Body.Close()
		defer releaseSettledReservation(reservationID, meta.usage.failed)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		completed := false
		conv := anthropicStreamToOpenAI{model: modelName}
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev map[string]json.RawMessage
			if json.Unmarshal([]byte(line[6:]), &ev) != nil {
				continue
			}
			chunks, text, done := conv.event(ev)
			if meta.mem != nil {
				reply.WriteString(text)
			}
			for _, ch := range chunks {
				if ttft == 0 && conv.streamedChars > 0 {
					ttft = time.Since(start).Milliseconds()
				}
				b, _ := json.Marshal(ch)
				meta.emit(w, "data: "+string(b))
				meta.emit(w, "")
			}
			if done {
				meta.emit(w, "data: [DONE]")
				meta.emit(w, "")
				completed = true
			}
			streamedChars = conv.streamedChars
			promptTok, completionTok, pc = conv.usage.prompt(), conv.usage.OutputTokens, conv.usage.cache()
			if len(chunks) > 0 || done {
				if err := w.Flush(); err != nil {
					logClientAbort(meta, userID, keyID, providerID, modelName, promptTok, completionTok, streamedChars, start, ttft, pc)
					return
				}
			}
			if completed {
				break
			}
		}
		latency := time.Since(start).Milliseconds()
		if !completed {
			errMsg := "upstream stream ended before completion"
			if err := scanner.Err(); err != nil {
				errMsg = "upstream stream error: " + truncateStr(err.Error(), 300)
			}
			// The provider bills for what it generated before the cut, so count
			// an estimate against the caps under the upstream's own status.
			promptTok, completionTok, _ = estimateIfMissing(meta, promptTok, completionTok, streamedChars)
			logUsageStream(meta, userID, keyID, providerID, modelName, "chat.completions", promptTok, completionTok, promptTok+completionTok, latency, ttft, resp.StatusCode, true, errMsg+"; usage estimated", pc)
			return
		}
		meta.mem.save(replyFromText(reply.String()))
		note := ""
		if p, cTok, est := estimateIfMissing(meta, promptTok, completionTok, streamedChars); est {
			promptTok, completionTok, note = p, cTok, "provider reported no usage; tokens estimated"
		}
		meta.rec.finishOK(providerID, modelName, promptTok, completionTok)
		logUsageStream(meta, userID, keyID, providerID, modelName, "chat.completions", promptTok, completionTok, promptTok+completionTok, latency, ttft, resp.StatusCode, true, note, pc)
	})
	return nil
}

func parseOpenAIUsage(body []byte) (prompt, completion, total, cached, reasoning int, costUSD float64, hasCost bool) {
	var u struct {
		Usage struct {
			PromptTokens        int      `json:"prompt_tokens"`
			CompletionTokens    int      `json:"completion_tokens"`
			TotalTokens         int      `json:"total_tokens"`
			Cost                *float64 `json:"cost"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &u)
	prompt = u.Usage.PromptTokens
	completion = u.Usage.CompletionTokens
	total = u.Usage.TotalTokens
	cached = u.Usage.PromptTokensDetails.CachedTokens
	reasoning = u.Usage.CompletionTokensDetails.ReasoningTokens
	if u.Usage.Cost != nil {
		costUSD = *u.Usage.Cost
		hasCost = true
	}
	return
}

// cacheWriteTokens reads prompt_tokens_details.cache_write_tokens, which
// OpenAI-compatible gateways in front of Anthropic report (and Spendmux does).
func cacheWriteTokens(body []byte) int {
	var u struct {
		Usage struct {
			PromptTokensDetails struct {
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &u)
	return u.Usage.PromptTokensDetails.CacheWriteTokens
}

func logUsage(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, provider, model, endpoint string, prompt, completion, total int, latencyMs, ttftMs int64, status int, streamed bool, errMsg string) {
	cost := providers.EstimateCostUSDMicros(provider, model, prompt, completion)
	logUsageFull(c, userID, keyID, provider, model, endpoint, prompt, completion, total, cost, latencyMs, ttftMs, status, streamed, errMsg, cacheTokens{}, 0)
}

func logUsageWithCost(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, provider, model, endpoint string, prompt, completion, total int, costMicros int64, latencyMs, ttftMs int64, status int, streamed bool, errMsg string) {
	logUsageFull(c, userID, keyID, provider, model, endpoint, prompt, completion, total, costMicros, latencyMs, ttftMs, status, streamed, errMsg, cacheTokens{}, 0)
}

// logUsageStream is logUsage for stream writers, where *fiber.Ctx is no longer valid.
func logUsageStream(meta streamMeta, userID uuid.UUID, keyID *uuid.UUID, provider, model, endpoint string, prompt, completion, total int, latencyMs, ttftMs int64, status int, streamed bool, errMsg string, pc cacheTokens) {
	cost := providers.EstimateCachedCostUSDMicros(provider, model, prompt, completion, pc.read, pc.write)
	logUsageWith(meta.usage, userID, keyID, provider, model, endpoint, prompt, completion, total, cost, latencyMs, ttftMs, status, streamed, errMsg, pc, 0)
}

func logUsageFull(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, provider, model, endpoint string, prompt, completion, total int, costMicros int64, latencyMs, ttftMs int64, status int, streamed bool, errMsg string, pc cacheTokens, reasoning int) {
	logUsageWith(usageCtxFrom(c), userID, keyID, provider, model, endpoint, prompt, completion, total, costMicros, latencyMs, ttftMs, status, streamed, errMsg, pc, reasoning)
}

// cacheTokens are the prompt tokens a provider read from or wrote to its
// prompt cache on one call.
type cacheTokens struct{ read, write int }

// usageCtx is the per-request context a usage row carries: the wallet
// headers and the trace the provider-call span joins.
type usageCtx struct {
	session, tenant string
	fingerprint     string // request hash, for counting identical repeats
	trace           context.Context
	reservation     uuid.UUID
	failed          *atomic.Bool
}

func usageCtxFrom(c *fiber.Ctx) usageCtx {
	if c == nil {
		return usageCtx{trace: context.Background()}
	}
	session, tenant := spendHeaders(c)
	fp, _ := c.Locals(requestHashLocal).(string)
	reservation, _ := c.Locals("reservation_id").(uuid.UUID)
	failed, _ := c.Locals("usage_persistence_failed").(*atomic.Bool)
	return usageCtx{session: session, tenant: tenant, fingerprint: fp, trace: telemetry.FromFiber(c), reservation: reservation, failed: failed}
}

func logUsageWith(uc usageCtx, userID uuid.UUID, keyID *uuid.UUID, provider, model, endpoint string, prompt, completion, total int, costMicros int64, latencyMs, ttftMs int64, status int, streamed bool, errMsg string, pc cacheTokens, reasoning int) {
	logUsageEvent(uc, models.UsageEvent{
		UserID:           userID,
		PlatformAPIKeyID: keyID,
		Provider:         provider,
		Model:            model,
		Endpoint:         endpoint,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		CostUSDMicros:    costMicros,
		LatencyMs:        latencyMs,
		TTFTMs:           ttftMs,
		StatusCode:       status,
		Streamed:         streamed,
		ErrorMessage:     errMsg,
		CachedTokens:     pc.read,
		CacheWriteTokens: pc.write,
		ReasoningTokens:  reasoning,
	})
}

// logUsageEvent prices, records and traces one usage row. Spend counts at
// list price; a cache hit costs nothing.
func logUsageEvent(uc usageCtx, ev models.UsageEvent) {
	if len(ev.ErrorMessage) > 500 {
		ev.ErrorMessage = ev.ErrorMessage[:500]
	}
	if ev.CacheHit {
		ev.CostUSDMicros, ev.ListCostUSDMicros = 0, 0
	} else {
		list := providers.EstimateCachedCostUSDMicros(ev.Provider, ev.Model, ev.PromptTokens, ev.CompletionTokens, ev.CachedTokens, ev.CacheWriteTokens)
		if list == 0 {
			list = ev.CostUSDMicros
		} else if ev.CachedTokens > 0 {
			// What prompt caching took off this call (writes cost a little extra).
			if full := providers.EstimateCostUSDMicros(ev.Provider, ev.Model, ev.PromptTokens, ev.CompletionTokens); full > list {
				ev.CostSavedUSDMicros = full - list
			}
		}
		ev.CostUSDMicros, ev.ListCostUSDMicros = list, list
	}
	ev.SessionID, ev.TenantID, ev.RequestHash = uc.session, uc.tenant, uc.fingerprint
	tokenID := ""
	if ev.PlatformAPIKeyID != nil {
		tokenID = ev.PlatformAPIKeyID.String()
	}
	trace := uc.trace
	if trace == nil {
		trace = context.Background()
	}
	latency := time.Duration(ev.LatencyMs) * time.Millisecond
	telemetry.RecordLLMCall(trace, telemetry.LLMCall{
		Endpoint: ev.Endpoint, Provider: ev.Provider, Model: ev.Model,
		Start: time.Now().Add(-latency), Latency: latency, TTFT: time.Duration(ev.TTFTMs) * time.Millisecond,
		PromptTokens: ev.PromptTokens, CompletionTokens: ev.CompletionTokens, CachedTokens: ev.CachedTokens, ReasoningTokens: ev.ReasoningTokens,
		CostUSD: float64(ev.CostUSDMicros) / 1e6, SavedUSD: float64(ev.CostSavedUSDMicros) / 1e6,
		Status: ev.StatusCode, Streamed: ev.Streamed, CacheHit: ev.CacheHit, Error: ev.ErrorMessage,
		TokenID: tokenID, Session: ev.SessionID, Tenant: ev.TenantID, Account: ev.UserID.String(),
	})
	usage.ApplyHistoryPolicy(&ev)
	if uc.reservation != uuid.Nil {
		ev.ReservationID = &uc.reservation
	}
	if err := actor.LogUsageEvent(ev); err != nil {
		if uc.failed != nil {
			uc.failed.Store(true)
		}
		slog.Error("usage persistence failed; retaining budget reservation", "error", err)
	}
}

// logClientAbort records a stream the client abandoned. The provider bills for
// what it generated up to the disconnect, so we log what we know: exact usage
// if the usage chunk had already arrived, otherwise a ~4 chars/token estimate
// of the relayed completion text. Status 499 marks it as client-closed.
func logClientAbort(meta streamMeta, userID uuid.UUID, keyID *uuid.UUID, provider, model string, promptTok, completionTok, streamedChars int, start time.Time, ttft int64, pc cacheTokens) {
	promptTok, completionTok, _ = estimateIfMissing(meta, promptTok, completionTok, streamedChars)
	logUsageStream(meta, userID, keyID, provider, model, "chat.completions", promptTok, completionTok, promptTok+completionTok,
		time.Since(start).Milliseconds(), ttft, 499, true, "client closed the connection mid-stream; usage estimated", pc)
}

// A stream owns cancellation until its body is closed by the writer.
type requestBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *requestBody) Close() error { b.cancel(); return b.ReadCloser.Close() }
func chatContext(req openAIChatRequest) context.Context {
	if req.ctx != nil {
		return req.ctx
	}
	return context.Background()
}
func releaseSettledReservation(id uuid.UUID, failed *atomic.Bool) {
	if failed == nil || !failed.Load() {
		keys.ReleaseTokenReservation(id)
	}
}
