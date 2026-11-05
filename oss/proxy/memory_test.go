package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeHistoryOrdersSystemHistoryThenNew(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"model":"gpt-oss-120b","provider":"groq","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"and my name?"}]}`)
	history := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"I am Asha"}`),
		json.RawMessage(`{"role":"assistant","content":"Hi Asha"}`),
	}
	out, pending, injected, err := mergeHistory(raw, history, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Provider string            `json:"provider"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	order := make([]string, len(got.Messages))
	for i, m := range got.Messages {
		order[i] = string(m)
	}
	joined := strings.Join(order, "|")
	if injected != 2 || len(got.Messages) != 4 || got.Provider != "groq" {
		t.Fatalf("merged %d (+%d injected): %s", len(got.Messages), injected, joined)
	}
	if !strings.Contains(order[0], "be brief") || !strings.Contains(order[1], "I am Asha") || !strings.Contains(order[3], "and my name?") {
		t.Fatalf("wrong order: %s", joined)
	}
	if len(pending) != 1 || !strings.Contains(string(pending[0]), "and my name?") {
		t.Fatalf("only the new non-system message should be stored, got %s", pending)
	}
}

func TestReplyExtraction(t *testing.T) {
	t.Parallel()
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"Asha","tool_calls":[{"id":"t1"}]}}]}`)
	if r := replyFromBody(body); !strings.Contains(string(r), "tool_calls") {
		t.Fatalf("reply should keep the full message, got %s", r)
	}
	if replyFromBody([]byte(`{"error":{}}`)) != nil || replyFromText("") != nil {
		t.Fatal("no reply to store")
	}
}
