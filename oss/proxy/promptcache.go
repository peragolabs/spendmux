package proxy

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/vrannang1/spendmux/models"
)

// Provider prompt caching (on by default). Anthropic bills a cached prompt
// prefix at a tenth of the input price but only caches what a request marks
// with cache_control. When a request carries no marks of its own, Spendmux
// adds them on Anthropic calls:
//
//   - the end of the system prompt (or the last tool when there is none),
//     since system prompts and tools repeat across calls;
//   - the end of the conversation, once it has more than one turn, so the
//     next turn reads everything before it from the cache.
//
// Answers do not change. A prompt below the model's minimum cacheable length
// is not cached and costs nothing extra. Turn it off per request with
// X-Spendmux-Prompt-Cache: off, or per token on the Keys page.
//
// Marks a client sends itself (cache_control on content parts, messages or
// tools, as Anthropic and LiteLLM clients do) are always passed to Anthropic
// and OpenRouter, and removed for other providers, which reject the field.

const promptCacheHeader = "X-Spendmux-Prompt-Cache"

func autoPromptCache(c *fiber.Ctx, pk models.PlatformAPIKey) bool {
	if pk.AutoCacheOff {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.Get(promptCacheHeader))) {
	case "off", "false", "0", "no", "none":
		return false
	}
	return true
}

var ephemeral = map[string]string{"type": "ephemeral"}

// cacheControlOf returns a part's, message's or tool's cache_control.
func cacheControlOf(m map[string]json.RawMessage) (json.RawMessage, bool) {
	cc, ok := m["cache_control"]
	if !ok || len(cc) == 0 || string(cc) == "null" {
		return nil, false
	}
	return cc, true
}

// markLast puts cache_control on the last block.
func markLast(blocks []map[string]interface{}, cc interface{}) bool {
	if len(blocks) == 0 {
		return false
	}
	blocks[len(blocks)-1]["cache_control"] = cc
	return true
}

// applyAutoBreakpoints marks the system prompt (or the last tool) and, for a
// multi-turn conversation, the last block of the last message.
func applyAutoBreakpoints(system, tools []map[string]interface{}, messages []map[string]interface{}) {
	if !markLast(system, ephemeral) {
		markLast(tools, ephemeral)
	}
	multiTurn := false
	for _, m := range messages {
		if m["role"] == "assistant" {
			multiTurn = true
			break
		}
	}
	if multiTurn && len(messages) > 0 {
		blocks, _ := messages[len(messages)-1]["content"].([]map[string]interface{})
		markLast(blocks, ephemeral)
	}
}

// keepsCacheControl reports whether an OpenAI-compatible provider accepts
// cache_control in chat requests.
func keepsCacheControl(providerID string) bool {
	return providerID == "openrouter"
}

// stripCacheControl removes cache_control from messages, their content
// parts and tools of a decoded OpenAI chat payload.
func stripCacheControl(payload map[string]interface{}) {
	if msgs, ok := payload["messages"].([]interface{}); ok {
		for _, m := range msgs {
			msg, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			delete(msg, "cache_control")
			if parts, ok := msg["content"].([]interface{}); ok {
				for _, p := range parts {
					if part, ok := p.(map[string]interface{}); ok {
						delete(part, "cache_control")
					}
				}
			}
		}
	}
	if tools, ok := payload["tools"].([]interface{}); ok {
		for _, t := range tools {
			if tool, ok := t.(map[string]interface{}); ok {
				delete(tool, "cache_control")
			}
		}
	}
}
