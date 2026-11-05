package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("bad json %s: %v", b, err)
	}
	return m
}

func TestAnthropicRequestMapsImagesStopAndToolChoice(t *testing.T) {
	raw := []byte(`{"model":"c","max_completion_tokens":50,"stop":"END","temperature":0.2,
	 "tool_choice":"required","parallel_tool_calls":false,
	 "tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],
	 "x_spendmux_anthropic":{"thinking":{"type":"enabled","budget_tokens":1024}},
	 "messages":[{"role":"system","content":[{"type":"text","text":"sys"}]},
	  {"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
	  {"role":"assistant","content":null,"tool_calls":[{"id":"t1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},
	  {"role":"tool","tool_call_id":"t1","content":"r1"},
	  {"role":"tool","tool_call_id":"t2","content":"r2"}]}`)
	out, err := anthropicRequest(openAIChatRequest{Raw: raw}, "c", false)
	if err != nil {
		t.Fatal(err)
	}
	p := decode(t, out)
	if p["system"] != "sys" || p["max_tokens"].(float64) != 50 || p["temperature"].(float64) != 0.2 {
		t.Fatalf("params: %v", p)
	}
	if seq := p["stop_sequences"].([]interface{}); seq[0] != "END" {
		t.Fatalf("stop: %v", p["stop_sequences"])
	}
	tc := p["tool_choice"].(map[string]interface{})
	if tc["type"] != "any" || tc["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice: %v", tc)
	}
	if p["thinking"] == nil {
		t.Fatal("thinking not passed through")
	}
	msgs := p["messages"].([]interface{})
	if len(msgs) != 3 {
		t.Fatalf("want user, assistant, merged tool results; got %d: %v", len(msgs), msgs)
	}
	img := msgs[0].(map[string]interface{})["content"].([]interface{})[1].(map[string]interface{})
	src := img["source"].(map[string]interface{})
	if img["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AAAA" {
		t.Fatalf("image: %v", img)
	}
	if results := msgs[2].(map[string]interface{})["content"].([]interface{}); len(results) != 2 {
		t.Fatalf("tool results should merge into one user turn: %v", results)
	}
}

func TestRewriteStripsAnthropicExtra(t *testing.T) {
	out, err := rewriteChatRequest([]byte(`{"model":"m","x_spendmux_anthropic":{"thinking":{}},"messages":[]}`), "m", false)
	if err != nil || strings.Contains(string(out), anthropicExtraField) {
		t.Fatalf("extra field leaked to OpenAI-compatible upstream: %s %v", out, err)
	}
}

func TestChatMessageFromAnthropicKeepsToolCallsAndStopReason(t *testing.T) {
	var anth anthropicMessage
	_ = json.Unmarshal([]byte(`{"id":"msg_1","stop_reason":"tool_use","content":[
	 {"type":"thinking","thinking":"hmm"},{"type":"text","text":"calling"},
	 {"type":"tool_use","id":"tu_1","name":"get","input":{"q":"x"}}],
	 "usage":{"input_tokens":10,"cache_read_input_tokens":90,"output_tokens":5}}`), &anth)
	msg, finish := chatMessageFromAnthropic(anth)
	if finish != "tool_calls" || msg["reasoning"] != "hmm" || msg["content"] != "calling" {
		t.Fatalf("msg=%v finish=%s", msg, finish)
	}
	calls := msg["tool_calls"].([]map[string]interface{})
	fn := calls[0]["function"].(map[string]interface{})
	if calls[0]["id"] != "tu_1" || fn["name"] != "get" || fn["arguments"] != `{"q":"x"}` {
		t.Fatalf("tool call: %v", calls)
	}
	if anth.Usage.prompt() != 100 {
		t.Fatalf("cached input tokens must count: %d", anth.Usage.prompt())
	}
}

func TestAnthropicStreamToOpenAIToolCalls(t *testing.T) {
	s := anthropicStreamToOpenAI{model: "c"}
	events := []string{
		`{"type":"message_start","message":{"id":"msg_9","usage":{"input_tokens":7}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu","name":"f"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	}
	var all []map[string]interface{}
	var done bool
	for _, e := range events {
		var ev map[string]json.RawMessage
		_ = json.Unmarshal([]byte(e), &ev)
		chunks, _, d := s.event(ev)
		all = append(all, chunks...)
		done = d
	}
	b, _ := json.Marshal(all)
	got := string(b)
	for _, want := range []string{`"content":"hi"`, `"id":"tu"`, `"name":"f"`, `"arguments":"{\"a\":"`, `"finish_reason":"tool_calls"`, `"prompt_tokens":7`, `"completion_tokens":4`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if !done {
		t.Error("message_stop should end the stream")
	}
}

func TestChatFromAnthropicToolRoundTrip(t *testing.T) {
	chat, stream, err := chatFromAnthropic([]byte(`{"model":"claude-x","max_tokens":100,"stream":true,
	 "system":[{"type":"text","text":"be brief"}],
	 "tools":[{"name":"get","input_schema":{"type":"object"}}],"tool_choice":{"type":"any"},
	 "messages":[{"role":"user","content":"weather?"},
	  {"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tu1","name":"get","input":{"city":"x"}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":[{"type":"text","text":"sunny"}]},{"type":"text","text":"thanks"}]}]}`))
	if err != nil || !stream {
		t.Fatalf("err=%v stream=%v", err, stream)
	}
	m := decode(t, chat)
	msgs := m["messages"].([]interface{})
	roles := []string{}
	for _, x := range msgs {
		roles = append(roles, x.(map[string]interface{})["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,user" {
		t.Fatalf("roles: %v", roles)
	}
	if m["tool_choice"] != "required" || m["max_tokens"].(float64) != 100 {
		t.Fatalf("params: %v", m)
	}
	tool := msgs[3].(map[string]interface{})
	if tool["tool_call_id"] != "tu1" || tool["content"] != "sunny" {
		t.Fatalf("tool message: %v", tool)
	}
}

func TestChatFromAnthropicRequiresMaxTokens(t *testing.T) {
	if _, _, err := chatFromAnthropic([]byte(`{"model":"c","messages":[{"role":"user","content":"hi"}]}`)); err == nil {
		t.Fatal("max_tokens is required by the Messages API")
	}
}

func TestAnthropicReplyFromChat(t *testing.T) {
	body := []byte(`{"id":"x1","model":"m","choices":[{"finish_reason":"tool_calls","message":{"content":"ok","tool_calls":[{"id":"c1","function":{"name":"f","arguments":"{\"a\":1}"}}]}}],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}}`)
	r := decode(t, anthropicReplyFromChat(200, body))
	if r["type"] != "message" || r["stop_reason"] != "tool_use" || r["id"] != "msg_x1" {
		t.Fatalf("%v", r)
	}
	content := r["content"].([]interface{})
	if content[1].(map[string]interface{})["input"].(map[string]interface{})["a"].(float64) != 1 {
		t.Fatalf("tool input: %v", content)
	}
	if u := r["usage"].(map[string]interface{}); u["input_tokens"].(float64) != 10 || u["cache_read_input_tokens"].(float64) != 2 {
		t.Fatalf("usage: %v", u)
	}
	e := decode(t, anthropicReplyFromChat(429, []byte(`{"error":{"message":"cap hit","code":"usd_budget_exceeded"}}`)))
	if e["type"] != "error" || e["error"].(map[string]interface{})["type"] != "rate_limit_error" {
		t.Fatalf("error shape: %v", e)
	}
}

func sseRun(t *testing.T, wr sseWriter, lines ...string) string {
	t.Helper()
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, l := range lines {
		wr.Line(w, l)
	}
	_ = w.Flush()
	return buf.String()
}

func TestAnthropicSSE(t *testing.T) {
	out := sseRun(t, &anthropicSSE{},
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant","content":""}}]}`, "",
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"f","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`data: [DONE]`)
	order := []string{"event: message_start", "event: content_block_start", `"text":"Hel"`, `"text":"lo"`, "event: content_block_stop",
		`"type":"tool_use"`, `"partial_json":"{}"`, `"stop_reason":"tool_use"`, `"output_tokens":2`, "event: message_stop"}
	pos := 0
	for _, want := range order {
		i := strings.Index(out[pos:], want)
		if i < 0 {
			t.Fatalf("missing %q after position %d in:\n%s", want, pos, out)
		}
		pos += i
	}
	if strings.Count(out, "event: message_start") != 1 {
		t.Fatal("message_start must be sent once")
	}
}

func TestChatFromResponses(t *testing.T) {
	chat, stream, err := chatFromResponses([]byte(`{"model":"gpt-x","instructions":"sys","max_output_tokens":20,
	 "tools":[{"type":"function","name":"f","parameters":{"type":"object"}}],
	 "text":{"format":{"type":"json_schema","name":"s","schema":{"type":"object"}}},
	 "input":[{"role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"https://x/y.png"}]},
	  {"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},
	  {"type":"function_call","call_id":"c2","name":"f","arguments":"{}"},
	  {"type":"function_call_output","call_id":"c1","output":"one"},
	  {"type":"function_call_output","call_id":"c2","output":"two"}]}`))
	if err != nil || stream {
		t.Fatalf("err=%v stream=%v", err, stream)
	}
	m := decode(t, chat)
	msgs := m["messages"].([]interface{})
	if len(msgs) != 5 {
		t.Fatalf("want system,user,assistant(2 calls),tool,tool; got %v", msgs)
	}
	if calls := msgs[2].(map[string]interface{})["tool_calls"].([]interface{}); len(calls) != 2 {
		t.Fatalf("consecutive function_call items should share one assistant turn: %v", msgs[2])
	}
	if m["max_tokens"].(float64) != 20 || m["response_format"].(map[string]interface{})["type"] != "json_schema" {
		t.Fatalf("params: %v", m)
	}
	if _, _, err := chatFromResponses([]byte(`{"model":"m","input":"hi","previous_response_id":"resp_1"}`)); err == nil {
		t.Fatal("previous_response_id must be refused")
	}
	if _, _, err := chatFromResponses([]byte(`{"model":"m","input":"hi","tools":[{"type":"web_search"}]}`)); err == nil {
		t.Fatal("hosted tools must be refused, not silently dropped")
	}
}

func TestResponsesReplyFromChat(t *testing.T) {
	body := []byte(`{"model":"m","choices":[{"finish_reason":"length","message":{"content":"partial","tool_calls":[{"id":"c1","function":{"name":"f","arguments":"{}"}}]}}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`)
	r := decode(t, responsesReplyFromChat(200, body))
	if r["object"] != "response" || r["status"] != "incomplete" || r["output_text"] != "partial" {
		t.Fatalf("%v", r)
	}
	out := r["output"].([]interface{})
	if out[0].(map[string]interface{})["type"] != "message" || out[1].(map[string]interface{})["call_id"] != "c1" {
		t.Fatalf("output: %v", out)
	}
	if u := r["usage"].(map[string]interface{}); u["total_tokens"].(float64) != 10 {
		t.Fatalf("usage: %v", u)
	}
}

func TestResponsesSSE(t *testing.T) {
	out := sseRun(t, &responsesSSE{created: 1},
		`data: {"model":"m","choices":[{"delta":{"role":"assistant","content":""}}]}`,
		`data: {"choices":[{"delta":{"content":"Hi"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":"{\"a\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		`data: [DONE]`)
	order := []string{"event: response.created", "event: response.output_item.added", `"delta":"Hi"`, "event: response.output_text.done",
		`"type":"function_call"`, "event: response.function_call_arguments.delta", `"arguments":"{\"a\":1}"`, "event: response.completed", `"total_tokens":7`}
	pos := 0
	for _, want := range order {
		i := strings.Index(out[pos:], want)
		if i < 0 {
			t.Fatalf("missing %q after position %d in:\n%s", want, pos, out)
		}
		pos += i
	}
	if strings.Count(out, `"sequence_number":0,`) != 1 || !strings.Contains(out, `"sequence_number":1,`) {
		t.Fatalf("events need increasing sequence numbers:\n%s", out)
	}
}
