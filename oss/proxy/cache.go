package proxy

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vrannang1/spendmux/internal/crypto"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/internal/providers"
	"github.com/vrannang1/spendmux/models"
)

// Response cache (opt-in). A request is cached when it sends
// X-Spendmux-Cache: <seconds>, or its token has a cache TTL:
//
//	X-Spendmux-Cache: 3600      read and write, keep for an hour
//	X-Spendmux-Cache: refresh   skip the read, store the fresh reply
//	X-Spendmux-Cache: off       neither (also: no-store, 0)
//	Cache-Control: no-cache     skip the read
//
// Entries are scoped to the account and keyed by provider, model and the
// canonical request body (stream flags and routing fields left out), so a
// streamed and a buffered call share an entry. Hits cost nothing, are logged
// with cache_hit and the cost they saved, and do not count against caps.
//
// Embeddings are the exception to opt-in: the same input always embeds to
// the same vector, so they are cached for a week by default unless the token
// turns automatic caching off (or the request sends X-Spendmux-Cache: off).

const (
	cacheHeader   = "X-Spendmux-Cache"
	maxCacheTTL   = 30 * 24 * time.Hour
	maxCacheBytes = 2 << 20
)

type cachePolicy struct {
	read, write bool
	ttl         time.Duration
}

func (p cachePolicy) active() bool { return p.read || p.write }

func cachePolicyFor(c *fiber.Ctx, pk models.PlatformAPIKey) cachePolicy {
	def := time.Duration(pk.CacheTTLSeconds) * time.Second
	if def > maxCacheTTL {
		def = maxCacheTTL
	}
	p := parseCachePolicy(c.Get(cacheHeader), def)
	if p.read && strings.Contains(strings.ToLower(c.Get(fiber.HeaderCacheControl)), "no-cache") {
		p.read = false
	}
	return p
}

const defaultEmbeddingsTTL = 7 * 24 * time.Hour

func embeddingsCachePolicy(c *fiber.Ctx, pk models.PlatformAPIKey) cachePolicy {
	if pk.CacheTTLSeconds <= 0 && !pk.AutoCacheOff {
		pk.CacheTTLSeconds = int64(defaultEmbeddingsTTL / time.Second)
	}
	return cachePolicyFor(c, pk)
}

func parseCachePolicy(header string, def time.Duration) cachePolicy {
	v := strings.ToLower(strings.TrimSpace(header))
	switch v {
	case "":
		if def <= 0 {
			return cachePolicy{}
		}
		return cachePolicy{read: true, write: true, ttl: def}
	case "off", "no-store", "false", "0":
		return cachePolicy{}
	case "refresh":
		if def <= 0 {
			def = time.Hour
		}
		return cachePolicy{write: true, ttl: def}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return cachePolicy{}
	}
	ttl := time.Duration(n) * time.Second
	if n > int64(maxCacheTTL/time.Second) {
		ttl = maxCacheTTL
	}
	return cachePolicy{read: true, write: true, ttl: ttl}
}

// volatileFields never change the reply, so they stay out of the cache key.
var volatileFields = []string{"stream", "stream_options", "provider", "fallback", "user", "metadata", "model"}

// canonicalRequest re-encodes a JSON body with sorted keys and no volatile
// fields, so formatting and field order do not split the cache.
func canonicalRequest(raw []byte) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for _, f := range volatileFields {
		delete(body, f)
	}
	return json.Marshal(body) // encoding/json sorts map keys
}

// cacheEntry is one request's slot in the cache: where it lives and the key
// that opens it.
type cacheEntry struct {
	policy   cachePolicy
	id       string
	key      [32]byte
	userID   uuid.UUID
	endpoint string
}

func newCacheEntry(policy cachePolicy, userID uuid.UUID, endpoint, provider, model string, raw []byte) *cacheEntry {
	if !policy.active() {
		return nil
	}
	canon, err := canonicalRequest(raw)
	if err != nil {
		return nil
	}
	parts := [][]byte{userID[:], []byte(endpoint), []byte(provider), []byte(model), canon}
	id := crypto.Derive("spendmux-cache-id", parts...)
	return &cacheEntry{
		policy:   policy,
		id:       hex.EncodeToString(id[:]),
		key:      crypto.Derive("spendmux-cache-key", parts...),
		userID:   userID,
		endpoint: endpoint,
	}
}

// requestHashLocal holds the request's fingerprint for its usage rows.
const requestHashLocal = "request_hash"

// requestFingerprint identifies a request the way the cache would, so the
// Usage page can count identical repeats a cache would have answered. It is a
// keyed hash: the prompt cannot be recovered from it.
func requestFingerprint(userID uuid.UUID, endpoint, provider, model string, raw []byte) string {
	canon, err := canonicalRequest(raw)
	if err != nil {
		return ""
	}
	h := crypto.Derive("spendmux-request-fp", userID[:], []byte(endpoint), []byte(provider), []byte(model), canon)
	return hex.EncodeToString(h[:16])
}

// cachedReply is a decrypted hit.
type cachedReply struct {
	row  models.ResponseCache
	body []byte
}

func (e *cacheEntry) lookup() (*cachedReply, bool) {
	if e == nil || !e.policy.read {
		return nil, false
	}
	var row models.ResponseCache
	err := database.Get().Where("id = ? AND user_id = ? AND expires_at > ?", e.id, e.userID, time.Now()).First(&row).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("cache_lookup_failed", "error", err)
		}
		return nil, false
	}
	body, err := crypto.OpenWith(e.key, row.Ciphertext)
	if err != nil {
		return nil, false
	}
	go func(id string) {
		database.Get().Model(&models.ResponseCache{}).Where("id = ?", id).UpdateColumn("hits", gorm.Expr("hits + 1"))
	}(row.ID)
	return &cachedReply{row: row, body: body}, true
}

// store saves a successful reply body (any "meta" envelope is dropped).
func (e *cacheEntry) store(provider, model string, body []byte, prompt, completion int) {
	if e == nil || !e.policy.write || len(body) == 0 || len(body) > maxCacheBytes {
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return
	}
	delete(envelope, "meta")
	clean, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	sealed, err := crypto.SealWith(e.key, clean)
	if err != nil {
		return
	}
	now := time.Now()
	row := models.ResponseCache{
		ID:               e.id,
		UserID:           e.userID,
		Endpoint:         e.endpoint,
		Provider:         provider,
		Model:            model,
		Ciphertext:       sealed,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CostUSDMicros:    providers.EstimateCostUSDMicros(provider, model, prompt, completion),
		CreatedAt:        now,
		ExpiresAt:        now.Add(e.policy.ttl),
	}
	err = database.Get().Exec(`INSERT INTO response_caches (id, user_id, endpoint, provider, model, ciphertext, prompt_tokens, completion_tokens, cost_usd_micros, hits, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT (id) DO UPDATE SET provider = EXCLUDED.provider, model = EXCLUDED.model, ciphertext = EXCLUDED.ciphertext,
			prompt_tokens = EXCLUDED.prompt_tokens, completion_tokens = EXCLUDED.completion_tokens, cost_usd_micros = EXCLUDED.cost_usd_micros,
			created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at`,
		row.ID, row.UserID, row.Endpoint, row.Provider, row.Model, row.Ciphertext, row.PromptTokens, row.CompletionTokens, row.CostUSDMicros, row.CreatedAt, row.ExpiresAt).Error
	if err != nil {
		slog.Warn("cache_store_failed", "error", err)
	}
}

// PurgeExpiredCache hard-deletes expired entries; the maintenance loop runs it.
func PurgeExpiredCache() error {
	return database.Get().Where("expires_at <= ?", time.Now()).Delete(&models.ResponseCache{}).Error
}

// withCacheMeta adds Spendmux's meta block to a cached reply body.
func withCacheMeta(body []byte, row models.ResponseCache, latency int64) []byte {
	var envelope map[string]interface{}
	if json.Unmarshal(body, &envelope) != nil {
		return body
	}
	envelope["meta"] = map[string]interface{}{
		"provider":   row.Provider,
		"cache":      "hit",
		"cost_usd":   0,
		"saved_usd":  float64(row.CostUSDMicros) / 1e6,
		"latency_ms": latency,
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return out
}

// logCacheHit records a hit: no cost, the original cost as saved.
func logCacheHit(c *fiber.Ctx, userID uuid.UUID, keyID *uuid.UUID, hit *cachedReply, latency int64, streamed bool) {
	r := hit.row
	logUsageEvent(usageCtxFrom(c), models.UsageEvent{
		UserID: userID, PlatformAPIKeyID: keyID, Provider: r.Provider, Model: r.Model, Endpoint: r.Endpoint,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens, TotalTokens: r.PromptTokens + r.CompletionTokens,
		LatencyMs: latency, StatusCode: 200, Streamed: streamed, CacheHit: true, CostSavedUSDMicros: r.CostUSDMicros,
	})
}

/* ── Streaming ───────────────────────────────────────────────────────── */

// replayStream sends a cached chat completion as an OpenAI chunk stream,
// through the same writer as live streams so /v1/messages and /v1/responses
// clients get their own event format.
func replayStream(c *fiber.Ctx, body []byte) error {
	meta := captureStreamMeta(c)
	lines := completionToChunks(body)
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		if meta.release != nil {
			defer meta.release()
		}
		for _, l := range lines {
			meta.emit(w, l)
			meta.emit(w, "")
		}
		_ = w.Flush()
	})
	return nil
}

// completionToChunks turns a chat.completion into the chunk lines a live
// stream would have produced: content, tool calls, finish, usage, [DONE].
func completionToChunks(body []byte) []string {
	var cmp struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   *string        `json:"content"`
				Reasoning string         `json:"reasoning,omitempty"`
				ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &cmp) != nil {
		return []string{"data: [DONE]"}
	}
	if cmp.Created == 0 {
		cmp.Created = time.Now().Unix()
	}
	chunk := func(choices []map[string]interface{}, usage json.RawMessage) string {
		m := map[string]interface{}{"id": cmp.ID, "object": "chat.completion.chunk", "created": cmp.Created, "model": cmp.Model, "choices": choices}
		if usage != nil {
			m["usage"] = usage
		}
		b, _ := json.Marshal(m)
		return "data: " + string(b)
	}
	var out []string
	finish := "stop"
	if len(cmp.Choices) > 0 {
		ch := cmp.Choices[0]
		delta := map[string]interface{}{"role": "assistant", "content": ""}
		if ch.Message.Content != nil {
			delta["content"] = *ch.Message.Content
		}
		if ch.Message.Reasoning != "" {
			delta["reasoning"] = ch.Message.Reasoning
		}
		out = append(out, chunk([]map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": nil}}, nil))
		if len(ch.Message.ToolCalls) > 0 {
			calls := make([]map[string]interface{}, len(ch.Message.ToolCalls))
			for i, tc := range ch.Message.ToolCalls {
				calls[i] = map[string]interface{}{"index": i, "id": tc.ID, "type": "function", "function": map[string]string{"name": tc.Function.Name, "arguments": tc.Function.Arguments}}
			}
			out = append(out, chunk([]map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"tool_calls": calls}, "finish_reason": nil}}, nil))
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
	}
	out = append(out, chunk([]map[string]interface{}{{"index": 0, "delta": map[string]interface{}{}, "finish_reason": finish}}, nil))
	if len(cmp.Usage) > 0 && string(cmp.Usage) != "null" {
		out = append(out, chunk([]map[string]interface{}{}, cmp.Usage))
	}
	return append(out, "data: [DONE]")
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// streamRecorder rebuilds a chat.completion from the chunk lines of a live
// stream, so a streamed reply can be cached like a buffered one.
type streamRecorder struct {
	entry     *cacheEntry
	id, model string
	created   int64
	content   strings.Builder
	reasoning strings.Builder
	calls     map[int]*chatToolCall
	finish    string
	usage     json.RawMessage
}

func newStreamRecorder(e *cacheEntry) *streamRecorder {
	if e == nil || !e.policy.write {
		return nil
	}
	return &streamRecorder{entry: e, calls: map[int]*chatToolCall{}}
}

func (r *streamRecorder) observe(line string) {
	if r == nil || !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
		return
	}
	var ch struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal([]byte(line[6:]), &ch) != nil {
		return
	}
	if r.id == "" {
		r.id, r.created = ch.ID, ch.Created
	}
	if ch.Model != "" {
		r.model = ch.Model
	}
	for _, c := range ch.Choices {
		r.content.WriteString(c.Delta.Content)
		r.reasoning.WriteString(c.Delta.Reasoning)
		for _, tc := range c.Delta.ToolCalls {
			call := r.calls[tc.Index]
			if call == nil {
				call = &chatToolCall{Type: "function"}
				r.calls[tc.Index] = call
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Function.Name != "" {
				call.Function.Name = tc.Function.Name
			}
			call.Function.Arguments += tc.Function.Arguments
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			r.finish = *c.FinishReason
		}
	}
	if len(ch.Usage) > 0 && string(ch.Usage) != "null" {
		r.usage = ch.Usage
	}
}

// completion assembles the recorded stream as a chat.completion body.
func (r *streamRecorder) completion() []byte {
	msg := map[string]interface{}{"role": "assistant", "content": r.content.String()}
	if r.reasoning.Len() > 0 {
		msg["reasoning"] = r.reasoning.String()
	}
	if len(r.calls) > 0 {
		idx := make([]int, 0, len(r.calls))
		for i := range r.calls {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		calls := make([]*chatToolCall, 0, len(idx))
		for _, i := range idx {
			calls = append(calls, r.calls[i])
		}
		msg["tool_calls"] = calls
	}
	finish := r.finish
	if finish == "" {
		finish = "stop"
	}
	if r.created == 0 {
		r.created = time.Now().Unix()
	}
	out := map[string]interface{}{
		"id": r.id, "object": "chat.completion", "created": r.created, "model": r.model,
		"choices": []map[string]interface{}{{"index": 0, "message": msg, "finish_reason": finish}},
	}
	if r.usage != nil {
		out["usage"] = r.usage
	}
	b, _ := json.Marshal(out)
	return b
}

// finishOK stores the recorded reply once the stream completed cleanly.
func (r *streamRecorder) finishOK(provider, model string, prompt, completion int) {
	if r == nil {
		return
	}
	r.entry.store(provider, model, r.completion(), prompt, completion)
}
