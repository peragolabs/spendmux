package proxy

import (
	"encoding/json"
	"testing"
)

func TestStreamRequestsAskForUsage(t *testing.T) {
	raw := []byte(`{"provider":"groq","model":"x","fallback":true,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := rewriteChatRequestOpts(raw, "m", true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	p := map[string]interface{}{}
	_ = json.Unmarshal(out, &p)
	if _, ok := p["provider"]; ok {
		t.Fatal("provider must be stripped")
	}
	opts, _ := p["stream_options"].(map[string]interface{})
	if opts == nil || opts["include_usage"] != true {
		t.Fatalf("expected stream_options.include_usage=true, got %v", p["stream_options"])
	}
	// retry variant drops it; non-stream never adds it
	out, _ = rewriteChatRequestOpts(raw, "m", true, false, false)
	p = map[string]interface{}{}
	_ = json.Unmarshal(out, &p)
	if _, ok := p["stream_options"]; ok {
		t.Fatal("retry must not send stream_options")
	}
	out, _ = rewriteChatRequestOpts(raw, "m", false, true, false)
	p = map[string]interface{}{}
	_ = json.Unmarshal(out, &p)
	if _, ok := p["stream_options"]; ok {
		t.Fatal("non-stream must not add stream_options")
	}
}
