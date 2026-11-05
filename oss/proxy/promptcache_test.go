package proxy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"

	"github.com/vrannang1/spendmux/models"
)

func marks(t *testing.T, v interface{}) int {
	t.Helper()
	b, _ := json.Marshal(v)
	return strings.Count(string(b), `"cache_control"`)
}

func TestAutoBreakpointsMarkSystemAndConversationTail(t *testing.T) {
	raw := []byte(`{"model":"c","messages":[{"role":"system","content":"big system prompt"},
	  {"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"next"}]}`)
	out, err := anthropicRequest(openAIChatRequest{Raw: raw, autoPromptCache: true}, "c", false)
	if err != nil {
		t.Fatal(err)
	}
	p := decode(t, out)
	sys := p["system"].([]interface{})
	if sys[0].(map[string]interface{})["cache_control"] == nil {
		t.Fatalf("system not marked: %v", p["system"])
	}
	msgs := p["messages"].([]interface{})
	last := msgs[len(msgs)-1].(map[string]interface{})["content"].([]interface{})
	if last[len(last)-1].(map[string]interface{})["cache_control"] == nil {
		t.Fatalf("tail not marked: %v", msgs)
	}
	if marks(t, p) != 2 {
		t.Fatalf("want 2 breakpoints, got %d", marks(t, p))
	}
}

func TestAutoBreakpointsSkipSingleTurnTailAndMarkToolsWithoutSystem(t *testing.T) {
	raw := []byte(`{"model":"c","tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}}],
	  "messages":[{"role":"user","content":"one-off question"}]}`)
	out, _ := anthropicRequest(openAIChatRequest{Raw: raw, autoPromptCache: true}, "c", false)
	p := decode(t, out)
	tools := p["tools"].([]interface{})
	if tools[1].(map[string]interface{})["cache_control"] == nil || marks(t, p) != 1 {
		t.Fatalf("want only the last tool marked: %s", out)
	}
}

func TestAutoBreakpointsOffKeepsRequestUnchanged(t *testing.T) {
	raw := []byte(`{"model":"c","messages":[{"role":"system","content":"s"},{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`)
	out, _ := anthropicRequest(openAIChatRequest{Raw: raw}, "c", false)
	p := decode(t, out)
	if p["system"] != "s" || marks(t, p) != 0 {
		t.Fatalf("auto off must add nothing: %s", out)
	}
}

func TestClientCacheControlWinsOverAuto(t *testing.T) {
	raw := []byte(`{"model":"c","messages":[
	  {"role":"system","content":[{"type":"text","text":"s","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
	  {"role":"user","content":"a"},{"role":"assistant","content":"b"},
	  {"role":"tool","tool_call_id":"t","content":"r","cache_control":{"type":"ephemeral"}},
	  {"role":"user","content":"c"}]}`)
	out, _ := anthropicRequest(openAIChatRequest{Raw: raw, autoPromptCache: true}, "c", false)
	p := decode(t, out)
	if marks(t, p) != 2 {
		t.Fatalf("client marks only (no auto): %s", out)
	}
	if !strings.Contains(string(out), `"ttl":"1h"`) {
		t.Fatalf("client mark not passed as-is: %s", out)
	}
}

func TestMessagesEndpointPassesCacheControlThrough(t *testing.T) {
	in := []byte(`{"model":"claude","max_tokens":10,
	  "system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],
	  "tools":[{"name":"f","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],
	  "messages":[{"role":"user","content":[{"type":"text","text":"q","cache_control":{"type":"ephemeral"}}]}]}`)
	chat, _, err := chatFromAnthropic(in)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(chat), `"cache_control"`); n != 3 {
		t.Fatalf("want 3 marks in chat form, got %d: %s", n, chat)
	}
	out, err := anthropicRequest(openAIChatRequest{Raw: chat, autoPromptCache: true}, "claude", false)
	if err != nil {
		t.Fatal(err)
	}
	if n := marks(t, decode(t, out)); n != 3 {
		t.Fatalf("want the same 3 marks sent to Anthropic, got %d: %s", n, out)
	}
	// Other providers reject the field.
	stripped, _ := rewriteChatRequest(chat, "gpt", false)
	if strings.Contains(string(stripped), "cache_control") {
		t.Fatalf("cache_control must be removed for OpenAI-compatible upstreams: %s", stripped)
	}
	kept, _ := rewriteChatRequestOpts(chat, "x", false, true, keepsCacheControl("openrouter"))
	if !strings.Contains(string(kept), "cache_control") {
		t.Fatal("openrouter accepts cache_control")
	}
}

func TestAnthropicUsageReportsCacheWrites(t *testing.T) {
	u := anthropicUsage{InputTokens: 10, CacheReadInputTokens: 900, CacheCreationInputTokens: 100, OutputTokens: 5}
	if u.prompt() != 1010 || u.cache() != (cacheTokens{read: 900, write: 100}) {
		t.Fatalf("usage: %d %+v", u.prompt(), u.cache())
	}
	body := []byte(`{"usage":{"prompt_tokens":1010,"prompt_tokens_details":{"cached_tokens":900,"cache_write_tokens":100}}}`)
	if cacheWriteTokens(body) != 100 {
		t.Fatal("cache_write_tokens not read")
	}
}

func TestRequestFingerprintIgnoresVolatileFields(t *testing.T) {
	u := uuid.New()
	a := requestFingerprint(u, "chat.completions", "openai", "gpt", []byte(`{"messages":[{"role":"user","content":"x"}],"stream":true}`))
	b := requestFingerprint(u, "chat.completions", "openai", "gpt", []byte(`{ "stream":false, "messages":[{"content":"x","role":"user"}]}`))
	c := requestFingerprint(u, "chat.completions", "openai", "gpt", []byte(`{"messages":[{"role":"user","content":"y"}]}`))
	if a == "" || a != b || a == c || len(a) != 32 {
		t.Fatalf("fingerprints: %q %q %q", a, b, c)
	}
}

func TestEmbeddingsCachedByDefault(t *testing.T) {
	app := fiber.New()
	ttl := func(pk models.PlatformAPIKey, header string) time.Duration {
		c := app.AcquireCtx(&fasthttp.RequestCtx{})
		defer app.ReleaseCtx(c)
		if header != "" {
			c.Request().Header.Set(cacheHeader, header)
		}
		return embeddingsCachePolicy(c, pk).ttl
	}
	if ttl(models.PlatformAPIKey{}, "") != defaultEmbeddingsTTL {
		t.Fatal("embeddings should cache by default")
	}
	if ttl(models.PlatformAPIKey{AutoCacheOff: true}, "") != 0 {
		t.Fatal("auto cache off must disable the default")
	}
	if ttl(models.PlatformAPIKey{}, "off") != 0 {
		t.Fatal("the off header must disable it")
	}
	if ttl(models.PlatformAPIKey{CacheTTLSeconds: 60}, "") != time.Minute {
		t.Fatal("token TTL wins")
	}
}
