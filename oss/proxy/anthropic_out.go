package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Outbound adapter: an OpenAI chat request becomes an Anthropic Messages
// request, and Anthropic's reply (buffered or streamed) becomes an OpenAI chat
// completion. Clients that speak Anthropic natively use /v1/messages, which
// converts the other way around this same pipeline (anthropic_in.go).

// anthropicExtraField carries Anthropic-only request fields (thinking) through
// the OpenAI-shaped pipeline. It is stripped before any OpenAI-compatible upstream.
const anthropicExtraField = "x_spendmux_anthropic"

// partText joins the text of an OpenAI content value (string or parts array).
func partText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		var typ, text string
		_ = json.Unmarshal(p["type"], &typ)
		if typ == "text" || typ == "input_text" || typ == "output_text" {
			_ = json.Unmarshal(p["text"], &text)
			b.WriteString(text)
		}
	}
	return b.String()
}

// anthropicContent converts an OpenAI user/assistant content value into
// Anthropic content blocks: text parts stay text, image_url parts become image
// blocks (data URLs as base64, others as url sources).
func anthropicContent(raw json.RawMessage) []map[string]interface{} {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []map[string]interface{}{{"type": "text", "text": s}}
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(parts))
	for _, p := range parts {
		var typ string
		_ = json.Unmarshal(p["type"], &typ)
		switch typ {
		case "text", "input_text", "output_text":
			var text string
			_ = json.Unmarshal(p["text"], &text)
			if text != "" {
				out = append(out, withCacheControl(map[string]interface{}{"type": "text", "text": text}, p))
			}
		case "image_url", "input_image":
			url := imageURL(p)
			if block := anthropicImage(url); block != nil {
				out = append(out, withCacheControl(block, p))
			}
		}
	}
	return out
}

// withCacheControl copies a source part's cache_control onto its block.
func withCacheControl(block map[string]interface{}, src map[string]json.RawMessage) map[string]interface{} {
	if cc, ok := cacheControlOf(src); ok {
		block["cache_control"] = cc
	}
	return block
}

// hasCacheControl reports whether any block carries cache_control.
func hasCacheControl(blocks []map[string]interface{}) bool {
	for _, b := range blocks {
		if _, ok := b["cache_control"]; ok {
			return true
		}
	}
	return false
}

// systemBlocks converts a system message's content to text blocks, keeping
// cache_control on its parts.
func systemBlocks(raw json.RawMessage) []map[string]interface{} {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []map[string]interface{}{{"type": "text", "text": s}}
	}
	var out []map[string]interface{}
	for _, b := range anthropicContent(raw) {
		if b["type"] == "text" {
			out = append(out, b)
		}
	}
	return out
}

// imageURL reads an image part's URL: {"image_url":{"url":...}} or {"image_url":"..."}.
func imageURL(p map[string]json.RawMessage) string {
	var obj struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(p["image_url"], &obj) == nil && obj.URL != "" {
		return obj.URL
	}
	var s string
	_ = json.Unmarshal(p["image_url"], &s)
	return s
}

func anthropicImage(url string) map[string]interface{} {
	if url == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		meta, data, ok := strings.Cut(rest, ",")
		mediaType, isBase64 := strings.CutSuffix(meta, ";base64")
		if !ok || !isBase64 {
			return nil
		}
		return map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": mediaType, "data": data}}
	}
	return map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "url", "url": url}}
}

func anthropicRequest(req openAIChatRequest, model string, stream bool) ([]byte, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(req.Raw, &source); err != nil {
		return nil, err
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(source["messages"], &messages); err != nil {
		return nil, fmt.Errorf("invalid messages")
	}
	var system []map[string]interface{}
	var out []map[string]interface{}
	marked := false // the client placed cache_control itself
	// add appends blocks, merging into the previous turn when the role repeats
	// (Anthropic wants user/assistant turns to alternate; tool results are user turns).
	add := func(role string, blocks []map[string]interface{}) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1]["role"] == role {
			out[n-1]["content"] = append(out[n-1]["content"].([]map[string]interface{}), blocks...)
			return
		}
		out = append(out, map[string]interface{}{"role": role, "content": blocks})
	}
	for _, message := range messages {
		var role string
		_ = json.Unmarshal(message["role"], &role)
		// A message-level cache_control (LiteLLM style) marks the message's last block.
		msgCC, msgMarked := cacheControlOf(message)
		finish := func(blocks []map[string]interface{}) []map[string]interface{} {
			if msgMarked {
				markLast(blocks, msgCC)
			}
			if hasCacheControl(blocks) {
				marked = true
			}
			return blocks
		}
		switch role {
		case "system", "developer":
			system = append(system, finish(systemBlocks(message["content"]))...)
		case "tool":
			var id string
			_ = json.Unmarshal(message["tool_call_id"], &id)
			result := map[string]interface{}{"type": "tool_result", "tool_use_id": id, "content": partText(message["content"])}
			// A mark on the tool output's parts marks the tool_result block.
			for _, b := range anthropicContent(message["content"]) {
				if cc, ok := b["cache_control"].(json.RawMessage); ok && !msgMarked {
					msgCC, msgMarked = cc, true
				}
			}
			add("user", finish([]map[string]interface{}{result}))
		case "user":
			add("user", finish(anthropicContent(message["content"])))
		case "assistant":
			blocks := anthropicContent(message["content"])
			var calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			_ = json.Unmarshal(message["tool_calls"], &calls)
			for _, call := range calls {
				var input interface{}
				if json.Unmarshal([]byte(call.Function.Arguments), &input) != nil || input == nil {
					input = map[string]interface{}{}
				}
				blocks = append(blocks, map[string]interface{}{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": input})
			}
			add("assistant", finish(blocks))
		default:
			return nil, fmt.Errorf("unsupported message role %q for Anthropic", role)
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		var mct int
		if json.Unmarshal(source["max_completion_tokens"], &mct) == nil && mct > 0 {
			maxTokens = mct
		} else {
			maxTokens = 4096
		}
	}
	payload := map[string]interface{}{"model": model, "messages": out, "max_tokens": maxTokens, "stream": stream}
	if len(system) > 0 && !hasCacheControl(system) && !req.autoPromptCache {
		texts := make([]string, len(system))
		for i, b := range system {
			texts[i], _ = b["text"].(string)
		}
		payload["system"] = strings.Join(texts, "\n")
	} else if len(system) > 0 {
		payload["system"] = system
	}
	for _, k := range []string{"temperature", "top_p"} {
		if v, ok := source[k]; ok && string(v) != "null" {
			payload[k] = v
		}
	}
	if stop := source["stop"]; len(stop) > 0 && string(stop) != "null" {
		var one string
		var many []string
		if json.Unmarshal(stop, &one) == nil && one != "" {
			payload["stop_sequences"] = []string{one}
		} else if json.Unmarshal(stop, &many) == nil && len(many) > 0 {
			payload["stop_sequences"] = many
		}
	}
	var tools []struct {
		Function struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			Parameters  interface{} `json:"parameters"`
		} `json:"function"`
		CacheControl json.RawMessage `json:"cache_control"`
	}
	_ = json.Unmarshal(source["tools"], &tools)
	var converted []map[string]interface{}
	if len(tools) > 0 {
		converted = make([]map[string]interface{}, 0, len(tools))
		for _, tool := range tools {
			schema := tool.Function.Parameters
			if schema == nil {
				schema = map[string]interface{}{"type": "object"}
			}
			t := map[string]interface{}{"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": schema}
			if len(tool.CacheControl) > 0 && string(tool.CacheControl) != "null" {
				t["cache_control"] = tool.CacheControl
				marked = true
			}
			converted = append(converted, t)
		}
		payload["tools"] = converted
		if choice := anthropicToolChoice(source["tool_choice"], source["parallel_tool_calls"]); choice != nil {
			payload["tool_choice"] = choice
		}
	}
	if req.autoPromptCache && !marked {
		applyAutoBreakpoints(system, converted, out)
	}
	var extra map[string]json.RawMessage
	if json.Unmarshal(source[anthropicExtraField], &extra) == nil {
		if t, ok := extra["thinking"]; ok {
			payload["thinking"] = t
		}
	}
	return json.Marshal(payload)
}

func anthropicToolChoice(raw, parallel json.RawMessage) map[string]interface{} {
	var choice map[string]interface{}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			choice = map[string]interface{}{"type": "auto"}
		case "required":
			choice = map[string]interface{}{"type": "any"}
		case "none":
			choice = map[string]interface{}{"type": "none"}
		}
	} else {
		var obj struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
			choice = map[string]interface{}{"type": "tool", "name": obj.Function.Name}
		}
	}
	if string(parallel) == "false" {
		if choice == nil {
			choice = map[string]interface{}{"type": "auto"}
		}
		if choice["type"] != "none" {
			choice["disable_parallel_tool_use"] = true
		}
	}
	return choice
}

// openAIFinishReason maps Anthropic stop_reason to OpenAI finish_reason.
func openAIFinishReason(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	default:
		return "stop"
	}
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// prompt counts every input token the provider billed, cached or not.
func (u anthropicUsage) prompt() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

func (u anthropicUsage) cache() cacheTokens {
	return cacheTokens{read: u.CacheReadInputTokens, write: u.CacheCreationInputTokens}
}

// details is the OpenAI prompt_tokens_details for this usage.
func (u anthropicUsage) details() map[string]interface{} {
	return map[string]interface{}{"cached_tokens": u.CacheReadInputTokens, "cache_write_tokens": u.CacheCreationInputTokens}
}

type anthropicMessage struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
}

// chatMessageFromAnthropic builds the OpenAI assistant message and finish reason.
func chatMessageFromAnthropic(anth anthropicMessage) (map[string]interface{}, string) {
	var text, thinking strings.Builder
	var calls []map[string]interface{}
	for _, block := range anth.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "tool_use":
			args := string(block.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, map[string]interface{}{
				"id": block.ID, "type": "function",
				"function": map[string]interface{}{"name": block.Name, "arguments": args},
			})
		}
	}
	msg := map[string]interface{}{"role": "assistant", "content": text.String()}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		if text.Len() == 0 {
			msg["content"] = nil
		}
	}
	if thinking.Len() > 0 {
		msg["reasoning"] = thinking.String()
	}
	return msg, openAIFinishReason(anth.StopReason)
}

// anthropicStreamToOpenAI turns Anthropic SSE events into OpenAI chat chunks.
type anthropicStreamToOpenAI struct {
	model         string
	id            string
	usage         anthropicUsage
	toolIndex     map[int]int // Anthropic content block index -> OpenAI tool_calls index
	finish        string
	streamedChars int
}

// event handles one Anthropic event and returns the OpenAI chunks it produces
// (JSON objects), text that belongs to the reply, and whether the stream ended.
func (s *anthropicStreamToOpenAI) event(ev map[string]json.RawMessage) (chunks []map[string]interface{}, text string, done bool) {
	var etype string
	_ = json.Unmarshal(ev["type"], &etype)
	chunk := func(delta map[string]interface{}, finish interface{}) map[string]interface{} {
		return map[string]interface{}{
			"id": s.id, "object": "chat.completion.chunk", "model": s.model,
			"choices": []map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	switch etype {
	case "message_start":
		var m struct {
			Message struct {
				ID    string         `json:"id"`
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
		}
		_ = json.Unmarshal(ev["message"], &m.Message)
		s.id = m.Message.ID
		if s.id == "" {
			s.id = "chatcmpl-anthropic"
		}
		s.usage = m.Message.Usage
		return []map[string]interface{}{chunk(map[string]interface{}{"role": "assistant", "content": ""}, nil)}, "", false
	case "content_block_start":
		var idx int
		_ = json.Unmarshal(ev["index"], &idx)
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(ev["content_block"], &block)
		if block.Type != "tool_use" {
			return nil, "", false
		}
		if s.toolIndex == nil {
			s.toolIndex = map[int]int{}
		}
		n := len(s.toolIndex)
		s.toolIndex[idx] = n
		return []map[string]interface{}{chunk(map[string]interface{}{"tool_calls": []map[string]interface{}{{
			"index": n, "id": block.ID, "type": "function", "function": map[string]interface{}{"name": block.Name, "arguments": ""},
		}}}, nil)}, "", false
	case "content_block_delta":
		var idx int
		_ = json.Unmarshal(ev["index"], &idx)
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		}
		_ = json.Unmarshal(ev["delta"], &d)
		switch d.Type {
		case "text_delta":
			if d.Text == "" {
				return nil, "", false
			}
			s.streamedChars += len(d.Text)
			return []map[string]interface{}{chunk(map[string]interface{}{"content": d.Text}, nil)}, d.Text, false
		case "thinking_delta":
			s.streamedChars += len(d.Thinking)
			return []map[string]interface{}{chunk(map[string]interface{}{"reasoning": d.Thinking}, nil)}, "", false
		case "input_json_delta":
			s.streamedChars += len(d.PartialJSON)
			return []map[string]interface{}{chunk(map[string]interface{}{"tool_calls": []map[string]interface{}{{
				"index": s.toolIndex[idx], "function": map[string]interface{}{"arguments": d.PartialJSON},
			}}}, nil)}, "", false
		}
	case "message_delta":
		var d struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(ev["delta"], &d)
		var u anthropicUsage
		if json.Unmarshal(ev["usage"], &u) == nil {
			if u.OutputTokens > 0 {
				s.usage.OutputTokens = u.OutputTokens
			}
			if u.InputTokens > 0 {
				s.usage.InputTokens = u.InputTokens
			}
			if u.CacheReadInputTokens > 0 {
				s.usage.CacheReadInputTokens = u.CacheReadInputTokens
			}
			if u.CacheCreationInputTokens > 0 {
				s.usage.CacheCreationInputTokens = u.CacheCreationInputTokens
			}
		}
		if d.StopReason != "" {
			s.finish = openAIFinishReason(d.StopReason)
			return []map[string]interface{}{chunk(map[string]interface{}{}, s.finish)}, "", false
		}
	case "message_stop":
		usage := map[string]interface{}{
			"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []interface{}{},
			"usage": map[string]interface{}{
				"prompt_tokens": s.usage.prompt(), "completion_tokens": s.usage.OutputTokens,
				"total_tokens":          s.usage.prompt() + s.usage.OutputTokens,
				"prompt_tokens_details": s.usage.details(),
			},
		}
		return []map[string]interface{}{usage}, "", true
	}
	return nil, "", false
}
