package upstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HumanizeError turns a provider's error body into one readable sentence.
// Handles the common shapes:
//
//	{"error":{"message":"…"}}            OpenAI, Groq, Cerebras, Mistral, …
//	{"error":{"type":"…","message":"…"}} Anthropic
//	[{"error":{"code":404,"message":"…","status":"NOT_FOUND"}}] Google
//	{"message":"…"} / {"detail":"…"}     misc
//	plain text / HTML                    → status text
func HumanizeError(provider string, status int, body []byte) string {
	msg := extractMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
		if msg == "" {
			msg = "request failed"
		}
	}
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if provider != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", provider, msg, status)
	}
	return fmt.Sprintf("%s (HTTP %d)", msg, status)
}

func extractMessage(body []byte) string {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return ""
	}
	// Google wraps in an array.
	if body[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(body, &arr) == nil && len(arr) > 0 {
			return extractMessage(arr[0])
		}
		return ""
	}
	if body[0] != '{' {
		// Not JSON (HTML error page, plain text): only keep short plain text.
		s := string(body)
		if strings.Contains(s, "<") || len(s) > 200 {
			return ""
		}
		return s
	}
	var m map[string]interface{}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if e, ok := m["error"]; ok {
		switch v := e.(type) {
		case string:
			return v
		case map[string]interface{}:
			if s, ok := v["message"].(string); ok && s != "" {
				return s
			}
		}
	}
	for _, k := range []string{"message", "detail", "error_description"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
