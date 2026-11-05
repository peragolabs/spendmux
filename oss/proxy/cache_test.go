package proxy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseCachePolicy(t *testing.T) {
	cases := []struct {
		header string
		def    time.Duration
		want   cachePolicy
	}{
		{"", 0, cachePolicy{}},
		{"", time.Minute, cachePolicy{read: true, write: true, ttl: time.Minute}},
		{"600", 0, cachePolicy{read: true, write: true, ttl: 10 * time.Minute}},
		{"off", time.Minute, cachePolicy{}},
		{"no-store", time.Minute, cachePolicy{}},
		{"refresh", 0, cachePolicy{write: true, ttl: time.Hour}},
		{"refresh", 5 * time.Minute, cachePolicy{write: true, ttl: 5 * time.Minute}},
		{"9999999999", 0, cachePolicy{read: true, write: true, ttl: maxCacheTTL}},
		{"soon", time.Minute, cachePolicy{}},
	}
	for _, c := range cases {
		if got := parseCachePolicy(c.header, c.def); got != c.want {
			t.Errorf("parseCachePolicy(%q, %v) = %+v, want %+v", c.header, c.def, got, c.want)
		}
	}
}

func TestCanonicalRequestIgnoresFormattingAndVolatileFields(t *testing.T) {
	a, err := canonicalRequest([]byte(`{"model":"x","stream":true,"messages":[{"role":"user","content":"hi"}],"temperature":0}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := canonicalRequest([]byte(`{ "temperature": 0, "messages": [ {"content": "hi", "role": "user"} ], "provider": "openai", "user": "u1" }`))
	if string(a) != string(b) {
		t.Fatalf("canonical forms differ:\n%s\n%s", a, b)
	}
	c, _ := canonicalRequest([]byte(`{"messages":[{"role":"user","content":"hi!"}],"temperature":0}`))
	if string(a) == string(c) {
		t.Fatal("different prompts must not share a key")
	}
}

func TestCacheEntryScopedByAccountAndRoute(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	p := cachePolicy{read: true, write: true, ttl: time.Minute}
	u1, u2 := uuid.New(), uuid.New()
	e1 := newCacheEntry(p, u1, "chat.completions", "openai", "gpt-4o", raw)
	if e1 == nil || len(e1.id) != 64 {
		t.Fatalf("bad entry %+v", e1)
	}
	for _, other := range []*cacheEntry{
		newCacheEntry(p, u2, "chat.completions", "openai", "gpt-4o", raw),
		newCacheEntry(p, u1, "chat.completions", "openai", "gpt-4o-mini", raw),
		newCacheEntry(p, u1, "chat.completions", "groq", "gpt-4o", raw),
	} {
		if other.id == e1.id || other.key == e1.key {
			t.Fatal("entries for different accounts or routes must differ")
		}
	}
	if newCacheEntry(cachePolicy{}, u1, "chat.completions", "openai", "gpt-4o", raw) != nil {
		t.Fatal("inactive policy should give no entry")
	}
}

func TestStreamRecorderRoundTripsThroughReplay(t *testing.T) {
	body := []byte(`{"id":"c1","object":"chat.completion","created":7,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Hello","tool_calls":[{"id":"t1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	lines := completionToChunks(body)
	if lines[len(lines)-1] != "data: [DONE]" {
		t.Fatalf("missing [DONE]: %v", lines)
	}
	rec := newStreamRecorder(&cacheEntry{policy: cachePolicy{write: true, ttl: time.Minute}})
	for _, l := range lines {
		rec.observe(l)
	}
	var got struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string         `json:"content"`
				ToolCalls []chatToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.completion(), &got); err != nil {
		t.Fatal(err)
	}
	ch := got.Choices[0]
	if got.ID != "c1" || got.Model != "m" || ch.Message.Content != "Hello" || ch.FinishReason != "tool_calls" || got.Usage.TotalTokens != 5 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if len(ch.Message.ToolCalls) != 1 || ch.Message.ToolCalls[0].Function.Arguments != `{"a":1}` || ch.Message.ToolCalls[0].ID != "t1" {
		t.Fatalf("tool call lost: %+v", ch.Message.ToolCalls)
	}
}

func TestNilRecorderIsSafe(t *testing.T) {
	var r *streamRecorder
	r.observe(`data: {"choices":[]}`)
	r.finishOK("p", "m", 1, 1)
}
