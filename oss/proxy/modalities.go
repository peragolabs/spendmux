package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/internal/upstream"
	"github.com/peragolabs/spendmux/models"
	"github.com/peragolabs/spendmux/oss/keys"
)

type modalityJSONRequest struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Raw      json.RawMessage `json:"-"`
}

// reserveModality runs the spend controls for a non-chat call once its
// price is known. It returns a non-zero status and body when the call must
// not leave the proxy; on success the reservation id is returned for release.
func reserveModality(c *fiber.Ctx, pk models.PlatformAPIKey, usd int64, priceKnown bool) (reservation uuid.UUID, status int, errBody fiber.Map) {
	tokens := int64(len(c.Body()))
	if tokens < 1 {
		tokens = 1
	}
	sessionID, tenantID := spendHeaders(c)
	spend, err := keys.EnforceSpendControls(keys.SpendRequest{
		Key:          pk,
		Tokens:       tokens,
		USDMicros:    usd,
		SessionID:    sessionID,
		TenantID:     tenantID,
		PriceUnknown: !priceKnown,
	})
	if err != nil {
		if errors.Is(err, keys.ErrPriceUnknown) {
			return uuid.Nil, 400, fiber.Map{"error": fiber.Map{
				"message": err.Error() + ". Use a model with a published price, or a token without a USD cap.",
				"type":    "invalid_request_error",
				"code":    "model_price_unknown",
			}}
		}
		code := "token_budget_exceeded"
		switch {
		case errors.Is(err, keys.ErrLoopDetected):
			code = "loop_detected"
		case errors.Is(err, keys.ErrSessionBudget):
			code = "session_budget_exceeded"
		case errors.Is(err, keys.ErrTenantBudget):
			code = "tenant_budget_exceeded"
		case errors.Is(err, keys.ErrDailyUSD), errors.Is(err, keys.ErrWeeklyUSD), errors.Is(err, keys.ErrMonthlyUSD):
			code = "usd_budget_exceeded"
		case errors.Is(err, keys.ErrPlatformKeyRateLimit):
			code = "rate_limit_exceeded"
		case errors.Is(err, keys.ErrTPMLimit):
			code = "tpm_exceeded"
		}
		return uuid.Nil, 429, fiber.Map{"error": fiber.Map{
			"message": err.Error(),
			"type":    "rate_limit_error",
			"code":    code,
		}}
	}
	if spend.SessionWarn {
		c.Set("X-Spendmux-Session-Warn", "80")
	}
	c.Locals("reservation_id", spend.ReservationID)
	c.Locals("usage_persistence_failed", &atomic.Bool{})
	return spend.ReservationID, 0, nil
}

// modalityPricer prices a JSON modality request before it is sent.
type modalityPricer func(providerID, modelName string, body []byte) (usd int64, known bool)

func priceImages(providerID, modelName string, body []byte) (int64, bool) {
	var req struct {
		N       int    `json:"n"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	_ = json.Unmarshal(body, &req)
	return providers.EstimateImageUSDMicros(providerID, modelName, req.N, req.Size, req.Quality)
}

func priceSpeech(providerID, modelName string, body []byte) (int64, bool) {
	var req struct {
		Input string `json:"input"`
	}
	_ = json.Unmarshal(body, &req)
	cost, known := providers.EstimateSpeechUSDMicros(providerID, modelName, utf8.RuneCountInString(req.Input))
	p, _ := providers.LookupPrice(providerID, modelName)
	// Output-audio token usage cannot be bounded from input characters.
	return cost, known && (p.PerChar > 0 || p.Source == "local")
}

func priceModeration(providerID, modelName string, body []byte) (int64, bool) {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(body, &req)
	return providers.EstimateCostUSDMicros(providerID, modelName, len(req.Input), 0), providers.PriceKnown(providerID, modelName)
}

// transcriptionSeconds estimates audio length from upload size before the
// call (~16 kB/s, a 128 kbps MP3; compressed formats only run longer), and
// is replaced by the provider's reported duration when it sends one.
func transcriptionSeconds(sizeBytes int64) float64 {
	s := float64(sizeBytes) / 16_000
	if s < 1 {
		s = 1
	}
	return s
}

// minAudioBytesPerSecond is below the lowest bitrate of the accepted upload
// formats (Opus/AMR voice codecs bottom out around 6 kbps = 750 B/s), so
// size / it is an upper bound on duration.
const minAudioBytesPerSecond = 500

func transcriptionBoundSeconds(sizeBytes int64) float64 {
	return max(float64(sizeBytes)/minAudioBytesPerSecond, 1)
}

// chargeUncertainModality counts a call whose outcome was never seen at its
// reserved estimate, marked for reconciliation.
func chargeUncertainModality(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, providerID, model, endpoint string, cost int64, reason string) {
	logUsageWithCost(c, userID, keyID, providerID, model, endpoint, 0, 0, 0, cost, 0, 0, 499, false,
		"upstream outcome unknown ("+truncateStr(reason, 120)+"); reserved estimate counted against caps, reconcile with provider usage")
}

// reportedAudioSeconds reads the duration from a transcription response
// (verbose_json "duration", or the newer "usage": {"seconds": N}).
func reportedAudioSeconds(body []byte) (float64, bool) {
	var r struct {
		Duration float64 `json:"duration"`
		Usage    struct {
			Seconds float64 `json:"seconds"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &r) != nil {
		return 0, false
	}
	if r.Usage.Seconds > 0 {
		return r.Usage.Seconds, true
	}
	if r.Duration > 0 {
		return r.Duration, true
	}
	return 0, false
}

func resolveModalityRoute(c *fiber.Ctx, pk models.PlatformAPIKey, pathPicker func(providers.Config) string, unsupportedMsg string) (cfg providers.Config, providerID, modelName, apiKey string, status int, errBody fiber.Map) {
	var envelope struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	raw := c.Body()
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return cfg, "", "", "", 400, fiber.Map{"error": fiber.Map{"message": "invalid json body", "type": "invalid_request_error"}}
	}
	providerID, modelName, err := providers.ResolveRouteWithDefault(envelope.Provider, envelope.Model, pk.AllowedProvider)
	if err != nil {
		return cfg, "", "", "", 400, fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}}
	}
	if err := keys.EnforceKeyScope(pk, providerID, modelName); err != nil {
		return cfg, "", "", "", 403, fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "key_scope_denied"}}
	}
	var ok bool
	cfg, ok = providers.Get(providerID)
	if !ok {
		return cfg, "", "", "", 400, fiber.Map{"error": fiber.Map{"message": "unknown provider: " + providerID, "type": "invalid_request_error"}}
	}
	if pathPicker(cfg) == "" {
		return cfg, "", "", "", 400, fiber.Map{"error": fiber.Map{"message": unsupportedMsg, "type": "invalid_request_error"}}
	}
	apiKey, err = ResolveAPIKey(pk.UserID, &cfg)
	if err != nil {
		return cfg, "", "", "", 400, fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}}
	}
	return cfg, providerID, modelName, apiKey, 0, nil
}

// rewriteUpstreamJSON injects model into the outbound body (provider stays Spendmux-only).
func rewriteUpstreamJSON(raw []byte, modelName string) ([]byte, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "provider")
	delete(m, "fallback")
	m["model"] = modelName
	return json.Marshal(m)
}

func proxyJSONModality(c *fiber.Ctx, endpoint string, pathPicker func(providers.Config) string, unsupportedMsg string, price modalityPricer) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	keyID := pk.ID
	cfg, providerID, modelName, apiKey, status, errBody := resolveModalityRoute(c, pk, pathPicker, unsupportedMsg)
	if status != 0 {
		return c.Status(status).JSON(errBody)
	}
	cost, known := price(providerID, modelName, c.Body())
	reservation, status, errBody := reserveModality(c, pk, cost, known)
	if status != 0 {
		return c.Status(status).JSON(errBody)
	}
	persistenceFailed := c.Locals("usage_persistence_failed").(*atomic.Bool)
	defer releaseSettledReservation(reservation, persistenceFailed)
	requestContext, cancelRequest := context.WithTimeout(c.UserContext(), 30*time.Minute)
	defer cancelRequest()

	outBody, err := rewriteUpstreamJSON(c.Body(), modelName)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "invalid json body", "type": "invalid_request_error"}})
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + pathPicker(cfg)
	start := time.Now()
	httpReq, _ := http.NewRequestWithContext(requestContext, "POST", url, bytes.NewReader(outBody))
	httpReq.Header.Set("Content-Type", "application/json")
	if err := setUpstreamAuth(httpReq, cfg, apiKey, outBody); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}

	resp, err := upstream.Do(httpReq)
	if err != nil {
		if upstream.MayHaveReachedProvider(err) {
			chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, endpoint, cost, err.Error())
		} else {
			logUsage(c, pk.UserID, &keyID, providerID, modelName, endpoint, 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream unavailable", "type": "api_error"}})
	}
	defer resp.Body.Close()
	respBody, err := upstream.ReadAllLimit(resp.Body, 32<<20)
	if err != nil {
		if resp.StatusCode < 300 || uncertainStatus(resp.StatusCode) {
			chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, endpoint, cost, err.Error())
		} else {
			logUsage(c, pk.UserID, &keyID, providerID, modelName, endpoint, 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream response too large or unreadable", "type": "api_error"}})
	}
	latency := time.Since(start).Milliseconds()
	if uncertainStatus(resp.StatusCode) {
		chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, endpoint, cost, fmt.Sprintf("upstream gateway status %d", resp.StatusCode))
		cost = 0
	} else if resp.StatusCode >= 300 {
		cost = 0 // the provider refused the call; nothing was billed
	}
	logUsageWithCost(c, pk.UserID, &keyID, providerID, modelName, endpoint, 0, 0, 0, cost, latency, 0, resp.StatusCode, false, "")

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Set("Content-Type", ct)
	return c.Status(resp.StatusCode).Send(respBody)
}

// V1ImagesGenerations proxies OpenAI-compatible image generation.
func V1ImagesGenerations(c *fiber.Ctx) error {
	return proxyJSONModality(c, "images.generations",
		func(cfg providers.Config) string { return cfg.ImagePath },
		"provider does not support image generation",
		priceImages,
	)
}

// V1AudioSpeech proxies TTS. Upstream may return audio bytes or JSON.
func V1AudioSpeech(c *fiber.Ctx) error {
	return proxyJSONModality(c, "audio.speech",
		func(cfg providers.Config) string { return cfg.SpeechPath },
		"provider does not support audio speech (TTS)",
		priceSpeech,
	)
}

// V1Moderations proxies content moderation.
func V1Moderations(c *fiber.Ctx) error {
	return proxyJSONModality(c, "moderations",
		func(cfg providers.Config) string { return cfg.ModerationPath },
		"provider does not support moderations",
		priceModeration,
	)
}

// V1AudioTranscriptions proxies STT. Accepts multipart form (file + provider + model + optional fields).
func V1AudioTranscriptions(c *fiber.Ctx) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	keyID := pk.ID

	provider := strings.ToLower(strings.TrimSpace(c.FormValue("provider")))
	modelName := strings.TrimSpace(c.FormValue("model"))
	providerID, modelName, err := providers.ResolveRouteWithDefault(provider, modelName, pk.AllowedProvider)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}
	if err := keys.EnforceKeyScope(pk, providerID, modelName); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error", "code": "key_scope_denied"}})
	}
	cfg, ok := providers.Get(providerID)
	if !ok || cfg.TranscriptionPath == "" {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "provider does not support audio transcriptions", "type": "invalid_request_error"}})
	}
	apiKey, err := ResolveAPIKey(pk.UserID, &cfg)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": `multipart field "file" is required`, "type": "invalid_request_error"}})
	}
	// File size bounds duration only through a minimum bitrate; reserve at
	// the lowest bitrate accepted formats use. Token-priced transcription
	// (audio tokens) has no such bound and stays refused under a USD cap.
	cost, _ := providers.EstimateTranscriptionUSDMicros(providerID, modelName, transcriptionBoundSeconds(fileHeader.Size))
	p, _ := providers.LookupPrice(providerID, modelName)
	known := p.Source == "local" || (p.Source == "table" && p.PerSecond > 0)
	reservation, status, errBody := reserveModality(c, pk, cost, known)
	if status != 0 {
		return c.Status(status).JSON(errBody)
	}
	persistenceFailed := c.Locals("usage_persistence_failed").(*atomic.Bool)
	defer releaseSettledReservation(reservation, persistenceFailed)
	requestContext, cancelRequest := context.WithTimeout(c.UserContext(), 30*time.Minute)
	defer cancelRequest()
	src, err := fileHeader.Open()
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "could not read upload", "type": "invalid_request_error"}})
	}
	defer src.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", modelName)
	for _, field := range []string{"language", "prompt", "response_format", "temperature"} {
		if v := strings.TrimSpace(c.FormValue(field)); v != "" {
			_ = w.WriteField(field, v)
		}
	}
	part, err := w.CreateFormFile("file", fileHeader.Filename)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fiber.Map{"message": "multipart build failed", "type": "api_error"}})
	}
	if _, err := io.Copy(part, io.LimitReader(src, 64<<20)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": "could not read upload", "type": "invalid_request_error"}})
	}
	_ = w.Close()
	body := buf.Bytes()

	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.TranscriptionPath
	start := time.Now()
	httpReq, _ := http.NewRequestWithContext(requestContext, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	// Multipart body hash for SigV4: sign the exact bytes we send.
	if err := setUpstreamAuth(httpReq, cfg, apiKey, body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}

	resp, err := upstream.Do(httpReq)
	if err != nil {
		if upstream.MayHaveReachedProvider(err) {
			chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", cost, err.Error())
		} else {
			logUsage(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream unavailable", "type": "api_error"}})
	}
	defer resp.Body.Close()
	respBody, err := upstream.ReadAllLimit(resp.Body, 8<<20)
	if err != nil {
		if resp.StatusCode < 300 || uncertainStatus(resp.StatusCode) {
			chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", cost, err.Error())
		} else {
			logUsage(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", 0, 0, 0, time.Since(start).Milliseconds(), 0, 502, false, err.Error())
		}
		return c.Status(502).JSON(fiber.Map{"error": fiber.Map{"message": "upstream response too large or unreadable", "type": "api_error"}})
	}
	latency := time.Since(start).Milliseconds()
	if secs, ok := reportedAudioSeconds(respBody); ok {
		cost, _ = providers.EstimateTranscriptionUSDMicros(providerID, modelName, secs)
	} else if reservation == uuid.Nil {
		// No cap: the typical-bitrate estimate is closer than the bound.
		cost, _ = providers.EstimateTranscriptionUSDMicros(providerID, modelName, transcriptionSeconds(fileHeader.Size))
	} // capped and no reported duration: keep the reserved bound
	if uncertainStatus(resp.StatusCode) {
		chargeUncertainModality(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", cost, fmt.Sprintf("upstream gateway status %d", resp.StatusCode))
		cost = 0
	} else if resp.StatusCode >= 300 {
		cost = 0
	}
	logUsageWithCost(c, pk.UserID, &keyID, providerID, modelName, "audio.transcriptions", 0, 0, 0, cost, latency, 0, resp.StatusCode, false, "")

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	c.Set("Content-Type", ct)
	return c.Status(resp.StatusCode).Send(respBody)
}
