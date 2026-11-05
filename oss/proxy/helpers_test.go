package proxy

import (
	"encoding/json"
	"testing"
)

func TestParseOpenAIModelIDs(t *testing.T) {
	t.Parallel()
	body := []byte(`{"object":"list","data":[{"id":"a"},{"id":"b"},{"id":"a"}]}`)
	ids := parseOpenAIModelIDs(body)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("%v", ids)
	}
	arr := []byte(`[{"id":"x"},{"id":"y"}]`)
	ids = parseOpenAIModelIDs(arr)
	if len(ids) != 2 {
		t.Fatalf("%v", ids)
	}
	if parseOpenAIModelIDs([]byte(`{}`)) != nil {
		t.Fatal("empty envelope")
	}
}

func TestRewriteUpstreamJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"provider":"openai","fallback":true,"model":"old","prompt":"hi","n":1}`)
	out, err := rewriteUpstreamJSON(raw, "gpt-image-1")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["provider"]; ok {
		t.Fatal("provider must be stripped")
	}
	if _, ok := m["fallback"]; ok {
		t.Fatal("fallback must be stripped")
	}
	if m["model"] != "gpt-image-1" || m["prompt"] != "hi" {
		t.Fatalf("%v", m)
	}
}

func TestRewriteChatRequestPreservesProviderFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"provider":"openai","fallback":true,"model":"old","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}],"response_format":{"type":"json_object"}}`)
	out, err := rewriteChatRequest(raw, "new-model", true)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["provider"]; ok {
		t.Fatal("provider must be stripped")
	}
	if _, ok := payload["fallback"]; ok {
		t.Fatal("fallback must be stripped")
	}
	if payload["model"] != "new-model" || payload["stream"] != true || payload["tools"] == nil || payload["response_format"] == nil {
		t.Fatalf("unexpected rewritten request: %v", payload)
	}
}

func TestAnthropicRequestConvertsTools(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"provider":"anthropic","model":"claude","messages":[{"role":"system","content":"be useful"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":7}"}}]},{"role":"tool","tool_call_id":"call_1","content":"done"}],"tools":[{"type":"function","function":{"name":"lookup","description":"look up","parameters":{"type":"object"}}}]}`)
	req := openAIChatRequest{Raw: raw}
	out, err := anthropicRequest(req, "claude", false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["system"] != "be useful" || payload["tools"] == nil {
		t.Fatalf("missing converted fields: %v", payload)
	}
	messages := payload["messages"].([]interface{})
	assistant := messages[0].(map[string]interface{})
	blocks := assistant["content"].([]interface{})
	if blocks[0].(map[string]interface{})["type"] != "tool_use" {
		t.Fatalf("tool call was not converted: %v", assistant)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []int{429, 502, 503, 504} {
		if !isRetryableStatus(s) {
			t.Fatalf("%d should be retryable", s)
		}
	}
	for _, s := range []int{200, 400, 401, 500} {
		if isRetryableStatus(s) {
			t.Fatalf("%d should not be retryable", s)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	t.Parallel()
	if truncateStr("  hi  ", 10) != "hi" {
		t.Fatal(truncateStr("  hi  ", 10))
	}
	long := stringsRepeat("a", 50)
	got := truncateStr(long, 10)
	if len(got) != 11 || !hasSuffix(got, "…") { // 10 chars + ellipsis rune may be multi-byte
		// ellipsis is one rune "…"
		if got != "aaaaaaaaaa…" {
			t.Fatalf("%q", got)
		}
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

func TestParseOpenAIUsageCachedAndReasoning(t *testing.T) {
	t.Parallel()
	body := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":8}}}`)
	prompt, completion, total, cached, reasoning, _, hasCost := parseOpenAIUsage(body)
	if prompt != 100 || completion != 20 || total != 120 || cached != 40 || reasoning != 8 || hasCost {
		t.Fatalf("prompt=%d completion=%d total=%d cached=%d reasoning=%d hasCost=%v", prompt, completion, total, cached, reasoning, hasCost)
	}
}
