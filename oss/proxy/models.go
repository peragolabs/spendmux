package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/internal/upstream"
	"github.com/peragolabs/spendmux/models"
	"github.com/peragolabs/spendmux/oss/keys"
)

// V1Models lists models for one provider (?provider=groq) or all providers
// the user has saved keys for. Auth: Authorization: Bearer sk-…
//
// Tries the upstream OpenAI-compatible GET {base}/models with the decrypted
// BYOK secret. If that fails (or the provider has no list API), falls back to
// SuggestedModels so the UI still has something to pick.
func V1Models(c *fiber.Ctx) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	if err := keys.EnforceWalletRateLimits(pk, 0); err != nil {
		return writeSpendError(c, err)
	}
	provider := strings.ToLower(strings.TrimSpace(c.Query("provider")))
	if provider == "" && pk.AllowedProvider != "" {
		provider = pk.AllowedProvider
	}
	if provider != "" {
		if err := keys.EnforceKeyScope(pk, provider, strings.TrimSpace(pk.AllowedModel)); err != nil && pk.AllowedProvider != "" {
			// If key is scoped, only allow listing that provider.
			if !strings.EqualFold(provider, pk.AllowedProvider) {
				return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "key_scope_denied"}})
			}
		}
	}
	return WriteModelsList(c, pk.UserID, provider)
}

// FallbackModels lists models for the /fallback picker (session cookie auth).
func FallbackModels(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}
	return WriteModelsList(c, userID, strings.ToLower(strings.TrimSpace(c.Query("provider"))))
}

// WriteModelsList is shared by /v1/models (bearer) and /fallback/models (session).
func WriteModelsList(c *fiber.Ctx, userID uuid.UUID, providerFilter string) error {
	var providerIDs []string
	if providerFilter != "" {
		if _, ok := providers.Get(providerFilter); !ok {
			return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "unknown provider: " + providerFilter, "type": "invalid_request_error"}})
		}
		providerIDs = []string{providerFilter}
	} else {
		var rows []models.ProviderKey
		database.Get().Where("user_id = ?", userID).Select("provider").Find(&rows)
		seen := map[string]bool{}
		for _, r := range rows {
			p := strings.ToLower(strings.TrimSpace(r.Provider))
			if p == "" || seen[p] {
				continue
			}
			if _, ok := providers.Get(p); !ok {
				continue
			}
			seen[p] = true
			providerIDs = append(providerIDs, p)
		}
		sort.Strings(providerIDs)
	}

	if len(providerIDs) == 0 {
		return c.JSON(fiber.Map{
			"object": "list",
			"data":   []interface{}{},
			"meta": fiber.Map{
				"message": "no provider keys saved — add one at /keys",
			},
		})
	}

	var data []fiber.Map
	sources := map[string]string{}
	errors := map[string]string{}

	for _, pid := range providerIDs {
		ids, source, listErr := listModelsForProvider(userID, pid)
		sources[pid] = source
		if listErr != nil {
			errors[pid] = listErr.Error()
		}
		for _, id := range ids {
			data = append(data, fiber.Map{
				"id":       id,
				"object":   "model",
				"created":  0,
				"owned_by": pid,
				"provider": pid,
			})
		}
	}

	meta := fiber.Map{"sources": sources}
	if providerFilter != "" {
		meta["provider"] = providerFilter
		meta["source"] = sources[providerFilter]
		if e, ok := errors[providerFilter]; ok {
			meta["upstream_error"] = e
		}
	} else if len(errors) > 0 {
		meta["upstream_errors"] = errors
	}

	return c.JSON(fiber.Map{
		"object": "list",
		"data":   data,
		"meta":   meta,
	})
}

// ListModelsForProvider fetches live model ids from the provider /models API
// (BYOK), falling back to SuggestedModels. Used by chat UI and /v1/models.
func ListModelsForProvider(userID uuid.UUID, providerID string) (ids []string, source string, upstreamErr error) {
	return listModelsForProvider(userID, providerID)
}

func listModelsForProvider(userID uuid.UUID, providerID string) (ids []string, source string, upstreamErr error) {
	cfg, ok := providers.Get(providerID)
	if !ok {
		return nil, "none", fmt.Errorf("unknown provider")
	}

	// Providers without an OpenAI-style /models — use suggestions only.
	if cfg.NativeAnthropic || cfg.ModelsPath == "-" {
		return providers.SuggestedModels(providerID), "suggested", nil
	}

	secret, err := ResolveAPIKey(userID, &cfg)
	if err != nil {
		sugs := providers.SuggestedModels(providerID)
		if len(sugs) > 0 {
			return sugs, "suggested", err
		}
		return nil, "none", err
	}

	path := cfg.ModelsPath
	if path == "" {
		path = "/models"
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + path

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return providers.SuggestedModels(providerID), "suggested", err
	}
	if err := setUpstreamAuth(req, cfg, secret, nil); err != nil {
		return providers.SuggestedModels(providerID), "suggested", err
	}

	resp, err := upstream.Do(req)
	if err != nil {
		return providers.SuggestedModels(providerID), "suggested", err
	}
	defer resp.Body.Close()
	body, readErr := upstream.ReadAllLimit(resp.Body, 4<<20)
	if readErr != nil {
		return providers.SuggestedModels(providerID), "suggested", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sugs := providers.SuggestedModels(providerID)
		return sugs, "suggested", fmt.Errorf("upstream %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}

	ids = parseOpenAIModelIDs(body)
	if len(ids) == 0 {
		sugs := providers.SuggestedModels(providerID)
		if len(sugs) > 0 {
			return sugs, "suggested", fmt.Errorf("upstream returned no model ids")
		}
		return nil, "upstream", nil
	}
	sort.Strings(ids)
	return ids, "upstream", nil
}

func parseOpenAIModelIDs(body []byte) []string {
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Data) > 0 {
		out := make([]string, 0, len(envelope.Data))
		seen := map[string]bool{}
		for _, m := range envelope.Data {
			id := strings.TrimSpace(m.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out
	}
	var arr []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &arr); err == nil {
		out := make([]string, 0, len(arr))
		seen := map[string]bool{}
		for _, m := range arr {
			id := strings.TrimSpace(m.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out
	}
	return nil
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
