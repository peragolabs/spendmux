package proxy

import (
	"encoding/json"
	"testing"
)

func TestMultiplicityRefusedOnlyUnderCaps(t *testing.T) {
	for _, raw := range []string{`{"n":2}`, `{"best_of":8}`, `{"n":1.5}`, `{"n":"2"}`} {
		req := openAIChatRequest{Raw: json.RawMessage(raw)}
		if applyCompletionPolicy(&req, true) == nil {
			t.Fatalf("capped request accepted unbounded multiplicity: %s", raw)
		}
		req = openAIChatRequest{Raw: json.RawMessage(raw)}
		if err := applyCompletionPolicy(&req, false); err != nil || req.limitEnforced {
			t.Fatalf("uncapped request must pass through unchanged: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"n":1}`, `{"n":null}`, `{}`} {
		req := openAIChatRequest{Raw: json.RawMessage(raw)}
		if err := applyCompletionPolicy(&req, true); err != nil || !req.limitEnforced {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}

func TestCompletionReservationCoversEveryLimitField(t *testing.T) {
	gpt := routeHop{Provider: "openai", Model: "gpt-4o"}
	o3 := routeHop{Provider: "openai", Model: "o3"}
	capped := func(raw string, maxTokens, mct int) openAIChatRequest {
		r := openAIChatRequest{Raw: json.RawMessage(raw), MaxTokens: maxTokens, MaxCompletionTokens: mct}
		r.completionLimit = max(maxTokens, mct)
		if err := applyCompletionPolicy(&r, true); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, tc := range []struct {
		name string
		req  openAIChatRequest
		hop  routeHop
		want int
	}{
		{"max_tokens", capped(`{"max_tokens":300}`, 300, 0), gpt, 300},
		{"max_completion_tokens", capped(`{"max_completion_tokens":700}`, 0, 700), o3, 700},
		{"both takes the larger", capped(`{"max_tokens":100,"max_completion_tokens":900}`, 100, 900), gpt, 900},
		{"default chat", capped(`{}`, 0, 0), gpt, 4096},
		{"default reasoning model", capped(`{}`, 0, 0), o3, 32768},
		{"default gpt-5", capped(`{}`, 0, 0), routeHop{"openai", "gpt-5-mini"}, 32768},
		{"reasoning_effort on any model", capped(`{"reasoning_effort":"high"}`, 0, 0), gpt, 32768},
		{"anthropic thinking", capped(`{"thinking":{"type":"enabled","budget_tokens":2000}}`, 0, 0), routeHop{"anthropic", "claude-sonnet-4-5"}, 32768},
	} {
		if got := completionReservation(tc.req, tc.hop); got != tc.want {
			t.Errorf("%s: reserved %d completion tokens, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEnforcedLimitUsesTheProvidersParameter(t *testing.T) {
	body := func(t *testing.T, provider, model, raw string, enforced bool) map[string]any {
		t.Helper()
		req := openAIChatRequest{limitEnforced: enforced, hopLimit: 512}
		out, err := withCompletionLimit([]byte(raw), provider, model, req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := body(t, "openai", "o3", `{"model":"o3"}`, true)
	if m["max_completion_tokens"] != float64(512) || m["max_tokens"] != nil {
		t.Fatalf("openai reasoning: %v", m)
	}
	m = body(t, "openai", "gpt-4o", `{"max_tokens":512}`, true)
	if m["max_completion_tokens"] != float64(512) || m["max_tokens"] != nil {
		t.Fatalf("openai: %v", m)
	}
	m = body(t, "groq", "llama-3.3-70b-versatile", `{"max_completion_tokens":9000}`, true)
	if m["max_tokens"] != float64(512) || m["max_completion_tokens"] != float64(512) {
		t.Fatalf("compatible provider must get max_tokens too: %v", m)
	}
	m = body(t, "azure", "gpt-4o", `{}`, true)
	if m["max_tokens"] != float64(512) || m["max_completion_tokens"] != nil {
		t.Fatalf("azure chat deployment: %v", m)
	}
	m = body(t, "azure", "o3-mini", `{}`, true)
	if m["max_completion_tokens"] != float64(512) || m["max_tokens"] != nil {
		t.Fatalf("azure reasoning deployment: %v", m)
	}
	// Uncapped: nothing is injected, but OpenAI's deprecated max_tokens is
	// renamed (its reasoning models reject it).
	m = body(t, "groq", "llama-3.3-70b-versatile", `{"model":"x"}`, false)
	if m["max_tokens"] != nil || m["max_completion_tokens"] != nil {
		t.Fatalf("uncapped request got a limit: %v", m)
	}
	m = body(t, "openai", "o3", `{"max_tokens":77}`, false)
	if m["max_completion_tokens"] != float64(77) || m["max_tokens"] != nil {
		t.Fatalf("openai rename: %v", m)
	}
}

func TestContentCostBoundedByBytes(t *testing.T) {
	msg := func(content string) []openAIMessage {
		return []openAIMessage{{Role: "user", Content: json.RawMessage(content)}}
	}
	for _, ok := range []string{`"hi"`, `null`, `[{"type":"text","text":"hi"}]`,
		`[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]`,
		`[{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]`} {
		if contentCostUnbounded(msg(ok)) {
			t.Errorf("bounded content refused: %s", ok)
		}
	}
	for _, bad := range []string{`[{"type":"image_url","image_url":{"url":"https://example.com/huge.png"}}]`,
		`[{"type":"file","file":{"file_id":"file-1"}}]`} {
		if !contentCostUnbounded(msg(bad)) {
			t.Errorf("remote media accepted as byte-bounded: %s", bad)
		}
	}
}

func TestTranscriptionBoundCoversLowBitrateAudio(t *testing.T) {
	// 1 MB of 6 kbps Opus is ~1333 s; the bound must not be below that.
	if got := transcriptionBoundSeconds(1_000_000); got < 1333 {
		t.Fatalf("bound %v s under-reserves low-bitrate audio", got)
	}
}

func TestTokenPricedSpeechCannotClaimBoundedAudioCost(t *testing.T) {
	if _, known := priceSpeech("openai", "gpt-4o-mini-tts", []byte(`{"input":"hello"}`)); known {
		t.Fatal("audio output guessed from text cannot authorize a dollar cap")
	}
	if _, known := priceSpeech("openai", "tts-1", []byte(`{"input":"hello"}`)); !known {
		t.Fatal("character-priced speech must remain supported")
	}
}
