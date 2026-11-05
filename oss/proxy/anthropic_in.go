package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// POST /v1/messages accepts Anthropic's Messages format (Claude Code, the
// Anthropic SDKs) and runs it through the same pipeline as chat completions:
// caps, memory, provider keys, fallback. Requests are converted to OpenAI chat
// on the way in and replies converted back, so any provider can serve them.
// A model without a provider (field, X-Spendmux-Provider header, token scope or
// "provider/model") is sent to Anthropic.

func V1Messages(c *fiber.Ctx) error {
	chat, stream, err := chatFromAnthropic(c.Body())
	if err != nil {
		return c.Status(400).JSON(anthropicError(400, err.Error(), ""))
	}
	c.Request().SetBody(chat)
	// Anthropic SDKs send the key as x-api-key; Spendmux tokens work either way.
	if c.Get("Authorization") == "" {
		if k := strings.TrimSpace(c.Get("x-api-key")); k != "" {
			c.Request().Header.Set("Authorization", "Bearer "+k)
		}
	}
	c.Locals(routeDefaultLocal, "anthropic")
	if stream {
		c.Locals(sseWriterLocal, &anthropicSSE{model: requestedModel(chat)})
	}
	if err := V1ChatCompletions(c); err != nil {
		return err
	}
	return rewriteJSONReply(c, anthropicReplyFromChat)
}

// rewriteJSONReply converts a buffered chat reply (success or error) with
// convert. Streamed replies were already converted by their sseWriter.
func rewriteJSONReply(c *fiber.Ctx, convert func(status int, body []byte) []byte) error {
	if strings.Contains(string(c.Response().Header.ContentType()), "event-stream") {
		return nil
	}
	body := convert(c.Response().StatusCode(), c.Response().Body())
	c.Response().SetBodyRaw(body)
	c.Set("Content-Type", "application/json")
	return nil
}

// requestedModel reads the model a converted chat request names, for stream
// events sent before (or without) the provider echoing one.
func requestedModel(chat []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(chat, &m)
	return m.Model
}

func chatFromAnthropic(raw []byte) (chat []byte, stream bool, err error) {
	var in struct {
		Model         string            `json:"model"`
		Provider      string            `json:"provider"`
		Fallback      bool              `json:"fallback"`
		MaxTokens     int               `json:"max_tokens"`
		Stream        bool              `json:"stream"`
		System        json.RawMessage   `json:"system"`
		Messages      []json.RawMessage `json:"messages"`
		Temperature   *float64          `json:"temperature"`
		TopP          *float64          `json:"top_p"`
		StopSequences []string          `json:"stop_sequences"`
		Tools         []struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			InputSchema  json.RawMessage `json:"input_schema"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"tools"`
		ToolChoice *struct {
			Type                   string `json:"type"`
			Name                   string `json:"name"`
			DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
		} `json:"tool_choice"`
		Thinking json.RawMessage `json:"thinking"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false, fmt.Errorf("invalid json body")
	}
	if len(in.Messages) == 0 {
		return nil, false, fmt.Errorf("messages: at least one message is required")
	}
	if in.MaxTokens <= 0 {
		return nil, false, fmt.Errorf("max_tokens: field required")
	}

	var messages []map[string]interface{}
	if sys := systemFromAnthropic(in.System); sys != nil {
		messages = append(messages, map[string]interface{}{"role": "system", "content": sys})
	}
	for _, m := range in.Messages {
		converted, err := chatMessagesFromAnthropic(m)
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, converted...)
	}

	out := map[string]interface{}{
		"model": in.Model, "messages": messages, "max_tokens": in.MaxTokens, "stream": in.Stream,
	}
	if in.Provider != "" {
		out["provider"] = in.Provider
	}
	if in.Fallback {
		out["fallback"] = true
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.StopSequences) > 0 {
		out["stop"] = in.StopSequences
	}
	if len(in.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(in.Tools))
		for _, t := range in.Tools {
			params := t.InputSchema
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object"}`)
			}
			tool := map[string]interface{}{"type": "function", "function": map[string]interface{}{
				"name": t.Name, "description": t.Description, "parameters": params,
			}}
			if isCacheControl(t.CacheControl) {
				tool["cache_control"] = t.CacheControl
			}
			tools = append(tools, tool)
		}
		out["tools"] = tools
	}
	if tc := in.ToolChoice; tc != nil {
		switch tc.Type {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "none":
			out["tool_choice"] = "none"
		case "tool":
			out["tool_choice"] = map[string]interface{}{"type": "function", "function": map[string]string{"name": tc.Name}}
		}
		if tc.DisableParallelToolUse {
			out["parallel_tool_calls"] = false
		}
	}
	if len(in.Thinking) > 0 && string(in.Thinking) != "null" {
		out[anthropicExtraField] = map[string]json.RawMessage{"thinking": in.Thinking}
	}
	chat, err = json.Marshal(out)
	return chat, in.Stream, err
}

func isCacheControl(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

// systemFromAnthropic converts the system prompt: a plain string when it
// carries no cache_control, otherwise text parts that keep their marks.
func systemFromAnthropic(raw json.RawMessage) interface{} {
	var blocks []struct {
		Type         string          `json:"type"`
		Text         string          `json:"text"`
		CacheControl json.RawMessage `json:"cache_control"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []map[string]interface{}
		marked := false
		for _, b := range blocks {
			if b.Type != "text" || b.Text == "" {
				continue
			}
			part := map[string]interface{}{"type": "text", "text": b.Text}
			if isCacheControl(b.CacheControl) {
				part["cache_control"] = b.CacheControl
				marked = true
			}
			parts = append(parts, part)
		}
		if marked {
			return parts
		}
	}
	if sys := anthropicText(raw); sys != "" {
		return sys
	}
	return nil
}

// anthropicText joins a string or text-block array (system prompts, tool results).
func anthropicText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// chatMessagesFromAnthropic converts one Anthropic message. A user turn with
// tool_result blocks becomes "tool" messages (which must directly follow the
// assistant tool calls) followed by any remaining user content.
func chatMessagesFromAnthropic(raw json.RawMessage) ([]map[string]interface{}, error) {
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("messages: invalid message")
	}
	if m.Role != "user" && m.Role != "assistant" {
		return nil, fmt.Errorf("messages: role must be user or assistant, got %q", m.Role)
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return []map[string]interface{}{{"role": m.Role, "content": s}}, nil
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
		Cache     json.RawMessage `json:"cache_control"`
		Source    struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
			URL       string `json:"url"`
		} `json:"source"`
	}
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("messages: content must be a string or an array of blocks")
	}
	var out, tools []map[string]interface{}
	var parts []map[string]interface{}
	var calls []map[string]interface{}
	var assistantCC json.RawMessage // assistant turns become one string, so a mark moves to the message
	for _, b := range blocks {
		var part map[string]interface{}
		switch b.Type {
		case "text":
			part = map[string]interface{}{"type": "text", "text": b.Text}
		case "image":
			url := b.Source.URL
			if b.Source.Type == "base64" {
				url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
			}
			if url != "" {
				part = map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": url}}
			}
		case "tool_use":
			args := string(b.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, map[string]interface{}{"id": b.ID, "type": "function", "function": map[string]string{"name": b.Name, "arguments": args}})
			if isCacheControl(b.Cache) {
				assistantCC = b.Cache
			}
		case "tool_result":
			text := anthropicText(b.Content)
			if b.IsError && text != "" {
				text = "Error: " + text
			}
			tool := map[string]interface{}{"role": "tool", "tool_call_id": b.ToolUseID, "content": text}
			if isCacheControl(b.Cache) {
				tool["cache_control"] = b.Cache
			}
			tools = append(tools, tool)
		}
		// thinking / redacted_thinking blocks are the model's own; they are not resent.
		if part != nil {
			if isCacheControl(b.Cache) {
				part["cache_control"] = b.Cache
				assistantCC = b.Cache
			}
			parts = append(parts, part)
		}
	}
	out = append(out, tools...)
	if m.Role == "assistant" {
		msg := map[string]interface{}{"role": "assistant", "content": joinTextParts(parts)}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		if assistantCC != nil {
			msg["cache_control"] = assistantCC
		}
		if len(calls) > 0 || len(parts) > 0 {
			out = append(out, msg)
		}
		return out, nil
	}
	if len(parts) > 0 {
		out = append(out, map[string]interface{}{"role": "user", "content": parts})
	}
	return out, nil
}

func joinTextParts(parts []map[string]interface{}) string {
	var b strings.Builder
	for _, p := range parts {
		if t, ok := p["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// anthropicStopReason maps OpenAI finish_reason to Anthropic stop_reason.
func anthropicStopReason(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	default:
		return "end_turn"
	}
}

// chatReply is the part of an OpenAI chat completion the format adapters read.
type chatReply struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	Meta json.RawMessage `json:"meta"`
}

func toolArgs(s string) json.RawMessage {
	if json.Valid([]byte(s)) && strings.TrimSpace(s) != "" {
		return json.RawMessage(s)
	}
	return json.RawMessage(`{}`)
}

func anthropicReplyFromChat(status int, body []byte) []byte {
	var r chatReply
	_ = json.Unmarshal(body, &r)
	if status >= 300 || r.Error != nil || len(r.Choices) == 0 {
		msg, code := "upstream error", ""
		if r.Error != nil {
			msg, code = r.Error.Message, r.Error.Code
		}
		out, _ := json.Marshal(anthropicError(status, msg, code))
		return out
	}
	ch := r.Choices[0]
	content := []map[string]interface{}{}
	if text := partText(ch.Message.Content); text != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	for _, tc := range ch.Message.ToolCalls {
		content = append(content, map[string]interface{}{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": toolArgs(tc.Function.Arguments)})
	}
	out := map[string]interface{}{
		"id": anthropicID(r.ID), "type": "message", "role": "assistant", "model": r.Model,
		"content": content, "stop_reason": anthropicStopReason(ch.FinishReason), "stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                r.Usage.PromptTokens - r.Usage.PromptTokensDetails.CachedTokens - r.Usage.PromptTokensDetails.CacheWriteTokens,
			"output_tokens":               r.Usage.CompletionTokens,
			"cache_read_input_tokens":     r.Usage.PromptTokensDetails.CachedTokens,
			"cache_creation_input_tokens": r.Usage.PromptTokensDetails.CacheWriteTokens,
		},
	}
	if len(r.Meta) > 0 {
		out["meta"] = r.Meta
	}
	b, _ := json.Marshal(out)
	return b
}

func anthropicID(id string) string {
	if strings.HasPrefix(id, "msg_") {
		return id
	}
	if id == "" {
		id = "spendmux"
	}
	return "msg_" + id
}

func anthropicError(status int, message, code string) fiber.Map {
	typ := "api_error"
	switch {
	case status == 400 || status == 413 || status == 422:
		typ = "invalid_request_error"
	case status == 401:
		typ = "authentication_error"
	case status == 403:
		typ = "permission_error"
	case status == 404:
		typ = "not_found_error"
	case status == 429:
		typ = "rate_limit_error"
	case status == 529:
		typ = "overloaded_error"
	}
	e := fiber.Map{"type": typ, "message": message}
	if code != "" {
		e["code"] = code
	}
	return fiber.Map{"type": "error", "error": e}
}

// anthropicSSE rewrites OpenAI chat chunks as Anthropic stream events.
type anthropicSSE struct {
	started   bool
	open      bool   // a content block is open
	openKind  string // "text" or "tool"
	block     int    // index of the open (or next) block
	toolBlock map[int]int
	stop      string
	usage     struct{ in, out, cached, written int }
	id, model string
}

func (a *anthropicSSE) send(w *bufio.Writer, event string, data map[string]interface{}) {
	data["type"] = event
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}

func (a *anthropicSSE) start(w *bufio.Writer) {
	if a.started {
		return
	}
	a.started = true
	a.send(w, "message_start", map[string]interface{}{"message": map[string]interface{}{
		"id": anthropicID(a.id), "type": "message", "role": "assistant", "model": a.model,
		"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
	}})
}

func (a *anthropicSSE) closeBlock(w *bufio.Writer) {
	if !a.open {
		return
	}
	a.send(w, "content_block_stop", map[string]interface{}{"index": a.block})
	a.open = false
	a.block++
}

func (a *anthropicSSE) Line(w *bufio.Writer, line string) {
	data, ok := strings.CutPrefix(line, "data: ")
	if !ok {
		return
	}
	if data == "[DONE]" {
		a.start(w)
		a.closeBlock(w)
		stop := a.stop
		if stop == "" {
			stop = "end_turn"
		}
		a.send(w, "message_delta", map[string]interface{}{
			"delta": map[string]interface{}{"stop_reason": stop, "stop_sequence": nil},
			"usage": map[string]int{"input_tokens": a.usage.in - a.usage.cached - a.usage.written, "output_tokens": a.usage.out, "cache_read_input_tokens": a.usage.cached, "cache_creation_input_tokens": a.usage.written},
		})
		a.send(w, "message_stop", map[string]interface{}{})
		return
	}
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
			Delta        struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return
	}
	if a.id == "" {
		a.id = chunk.ID
	}
	if chunk.Model != "" {
		a.model = chunk.Model
	}
	a.start(w)
	if chunk.Usage != nil {
		a.usage.in, a.usage.out, a.usage.cached = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.PromptTokensDetails.CachedTokens
		a.usage.written = chunk.Usage.PromptTokensDetails.CacheWriteTokens
	}
	for _, ch := range chunk.Choices {
		if t := ch.Delta.Content; t != nil && *t != "" {
			if !a.open || a.openKind != "text" {
				a.closeBlock(w)
				a.send(w, "content_block_start", map[string]interface{}{"index": a.block, "content_block": map[string]string{"type": "text", "text": ""}})
				a.open, a.openKind = true, "text"
			}
			a.send(w, "content_block_delta", map[string]interface{}{"index": a.block, "delta": map[string]string{"type": "text_delta", "text": *t}})
		}
		for _, tc := range ch.Delta.ToolCalls {
			if a.toolBlock == nil {
				a.toolBlock = map[int]int{}
			}
			if _, seen := a.toolBlock[tc.Index]; !seen {
				a.closeBlock(w)
				a.toolBlock[tc.Index] = a.block
				a.send(w, "content_block_start", map[string]interface{}{"index": a.block, "content_block": map[string]interface{}{
					"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]interface{}{},
				}})
				a.open, a.openKind = true, "tool"
			}
			if tc.Function.Arguments != "" && a.toolBlock[tc.Index] == a.block && a.open {
				a.send(w, "content_block_delta", map[string]interface{}{"index": a.block, "delta": map[string]string{"type": "input_json_delta", "partial_json": tc.Function.Arguments}})
			}
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			a.stop = anthropicStopReason(*ch.FinishReason)
		}
	}
}
