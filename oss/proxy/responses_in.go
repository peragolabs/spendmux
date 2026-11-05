package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// POST /v1/responses accepts OpenAI's Responses format (newer OpenAI SDK code
// and agent frameworks) and runs it through the chat pipeline, like
// /v1/messages. Stateless only: previous_response_id is refused (send the full
// input, or use X-Spendmux-Memory). A model without a provider goes to OpenAI.

func V1Responses(c *fiber.Ctx) error {
	chat, stream, err := chatFromResponses(c.Body())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": err.Error(), "type": "invalid_request_error"}})
	}
	c.Request().SetBody(chat)
	c.Locals(routeDefaultLocal, "openai")
	if stream {
		c.Locals(sseWriterLocal, &responsesSSE{created: time.Now().Unix(), model: requestedModel(chat)})
	}
	if err := V1ChatCompletions(c); err != nil {
		return err
	}
	return rewriteJSONReply(c, responsesReplyFromChat)
}

func chatFromResponses(raw []byte) (chat []byte, stream bool, err error) {
	var in struct {
		Model              string          `json:"model"`
		Provider           string          `json:"provider"`
		Fallback           bool            `json:"fallback"`
		Input              json.RawMessage `json:"input"`
		Instructions       string          `json:"instructions"`
		MaxOutputTokens    int             `json:"max_output_tokens"`
		Temperature        *float64        `json:"temperature"`
		TopP               *float64        `json:"top_p"`
		Stream             bool            `json:"stream"`
		PreviousResponseID string          `json:"previous_response_id"`
		ParallelToolCalls  *bool           `json:"parallel_tool_calls"`
		ToolChoice         json.RawMessage `json:"tool_choice"`
		Tools              []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Text *struct {
			Format json.RawMessage `json:"format"`
		} `json:"text"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false, fmt.Errorf("invalid json body")
	}
	if in.PreviousResponseID != "" {
		return nil, false, fmt.Errorf("previous_response_id is not supported: Spendmux does not store responses. Send the full input, or set X-Spendmux-Memory to keep the conversation")
	}

	var messages []map[string]interface{}
	if in.Instructions != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": in.Instructions})
	}
	items, err := chatMessagesFromResponsesInput(in.Input)
	if err != nil {
		return nil, false, err
	}
	messages = append(messages, items...)
	if len(messages) == 0 || (len(messages) == 1 && in.Instructions != "") {
		return nil, false, fmt.Errorf("input is required")
	}

	out := map[string]interface{}{"model": in.Model, "messages": messages, "stream": in.Stream}
	if in.Provider != "" {
		out["provider"] = in.Provider
	}
	if in.Fallback {
		out["fallback"] = true
	}
	if in.MaxOutputTokens > 0 {
		out["max_tokens"] = in.MaxOutputTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if in.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *in.ParallelToolCalls
	}
	var tools []map[string]interface{}
	for _, t := range in.Tools {
		if t.Type != "function" {
			return nil, false, fmt.Errorf("tools: only function tools are supported, got %q", t.Type)
		}
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, map[string]interface{}{"type": "function", "function": map[string]interface{}{
			"name": t.Name, "description": t.Description, "parameters": params,
		}})
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	if len(in.ToolChoice) > 0 && string(in.ToolChoice) != "null" {
		var s string
		var obj struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal(in.ToolChoice, &s) == nil {
			out["tool_choice"] = s
		} else if json.Unmarshal(in.ToolChoice, &obj) == nil && obj.Type == "function" {
			out["tool_choice"] = map[string]interface{}{"type": "function", "function": map[string]string{"name": obj.Name}}
		}
	}
	if in.Text != nil && len(in.Text.Format) > 0 {
		if rf := responseFormat(in.Text.Format); rf != nil {
			out["response_format"] = rf
		}
	}
	chat, err = json.Marshal(out)
	return chat, in.Stream, err
}

// responseFormat maps text.format ({"type":"json_schema","name",...}) to chat's response_format.
func responseFormat(raw json.RawMessage) map[string]interface{} {
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	var typ string
	_ = json.Unmarshal(f["type"], &typ)
	switch typ {
	case "json_object":
		return map[string]interface{}{"type": "json_object"}
	case "json_schema":
		schema := map[string]json.RawMessage{}
		for _, k := range []string{"name", "schema", "strict", "description"} {
			if v, ok := f[k]; ok {
				schema[k] = v
			}
		}
		return map[string]interface{}{"type": "json_schema", "json_schema": schema}
	}
	return nil
}

// chatMessagesFromResponsesInput converts input (a string or a list of items).
// Consecutive function_call items become one assistant message with tool_calls;
// function_call_output items become tool messages.
func chatMessagesFromResponsesInput(raw json.RawMessage) ([]map[string]interface{}, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil, nil
		}
		return []map[string]interface{}{{"role": "user", "content": s}}, nil
	}
	var items []struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("input must be a string or an array of items")
	}
	var out []map[string]interface{}
	var pending map[string]interface{} // assistant message collecting tool calls
	flush := func() {
		if pending != nil {
			out = append(out, pending)
			pending = nil
		}
	}
	for _, it := range items {
		switch {
		case it.Type == "function_call":
			if pending == nil {
				pending = map[string]interface{}{"role": "assistant", "content": nil, "tool_calls": []map[string]interface{}{}}
			}
			pending["tool_calls"] = append(pending["tool_calls"].([]map[string]interface{}), map[string]interface{}{
				"id": it.CallID, "type": "function", "function": map[string]string{"name": it.Name, "arguments": it.Arguments},
			})
		case it.Type == "function_call_output":
			flush()
			out = append(out, map[string]interface{}{"role": "tool", "tool_call_id": it.CallID, "content": responsesOutputText(it.Output)})
		case it.Type == "message" || (it.Type == "" && it.Role != ""):
			flush()
			role := it.Role
			if role == "developer" {
				role = "system"
			}
			if role != "user" && role != "assistant" && role != "system" {
				return nil, fmt.Errorf("input: unsupported role %q", it.Role)
			}
			out = append(out, map[string]interface{}{"role": role, "content": chatContentFromResponses(it.Content, role)})
		case it.Type == "reasoning":
			// The model's own reasoning summary; not resent.
		default:
			return nil, fmt.Errorf("input: unsupported item type %q", it.Type)
		}
	}
	flush()
	return out, nil
}

func responsesOutputText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return partText(raw)
}

// chatContentFromResponses maps input_text/output_text/input_image parts to chat parts.
func chatContentFromResponses(raw json.RawMessage, role string) interface{} {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	if role != "user" {
		return partText(raw) // system and assistant turns carry text only
	}
	out := make([]map[string]interface{}, 0, len(parts))
	for _, p := range parts {
		var typ, text string
		_ = json.Unmarshal(p["type"], &typ)
		switch typ {
		case "input_text", "output_text", "text":
			_ = json.Unmarshal(p["text"], &text)
			out = append(out, map[string]interface{}{"type": "text", "text": text})
		case "input_image":
			if url := imageURL(p); url != "" {
				out = append(out, map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": url}})
			}
		}
	}
	return out
}

func newItemID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// responseObject builds a Responses API object from the reply's parts.
func responseObject(id, model string, created int64, text string, calls []map[string]interface{}, finish string, in, out, cached int) map[string]interface{} {
	output := []map[string]interface{}{}
	if text != "" {
		output = append(output, map[string]interface{}{
			"type": "message", "id": newItemID("msg_"), "status": "completed", "role": "assistant",
			"content": []map[string]interface{}{{"type": "output_text", "text": text, "annotations": []interface{}{}}},
		})
	}
	output = append(output, calls...)
	resp := map[string]interface{}{
		"id": id, "object": "response", "created_at": created, "status": "completed", "model": model,
		"output": output, "output_text": text, "error": nil, "incomplete_details": nil,
		"usage": map[string]interface{}{
			"input_tokens": in, "output_tokens": out, "total_tokens": in + out,
			"input_tokens_details": map[string]int{"cached_tokens": cached},
		},
	}
	switch finish {
	case "length":
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	case "content_filter":
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]string{"reason": "content_filter"}
	}
	return resp
}

func responsesReplyFromChat(status int, body []byte) []byte {
	var r chatReply
	_ = json.Unmarshal(body, &r)
	if status >= 300 || r.Error != nil || len(r.Choices) == 0 {
		return body // already OpenAI's error shape
	}
	ch := r.Choices[0]
	var calls []map[string]interface{}
	for _, tc := range ch.Message.ToolCalls {
		calls = append(calls, map[string]interface{}{
			"type": "function_call", "id": newItemID("fc_"), "call_id": tc.ID, "name": tc.Function.Name,
			"arguments": tc.Function.Arguments, "status": "completed",
		})
	}
	resp := responseObject(newItemID("resp_"), r.Model, time.Now().Unix(), partText(ch.Message.Content), calls, ch.FinishReason,
		r.Usage.PromptTokens, r.Usage.CompletionTokens, r.Usage.PromptTokensDetails.CachedTokens)
	if len(r.Meta) > 0 {
		resp["meta"] = r.Meta
	}
	b, _ := json.Marshal(resp)
	return b
}

// responsesSSE rewrites OpenAI chat chunks as Responses stream events.
type responsesSSE struct {
	created   int64
	id, model string
	seq       int
	started   bool
	textOpen  bool
	textID    string
	text      strings.Builder
	outIndex  int                    // next output_index
	textIndex int                    // output_index of the message item
	calls     map[int]*responsesCall // chat tool index -> call
	order     []int
	finish    string
	usage     struct{ in, out, cached int }
}

type responsesCall struct {
	itemID, callID, name string
	args                 strings.Builder
	index                int
}

func (r *responsesSSE) send(w *bufio.Writer, event string, data map[string]interface{}) {
	data["type"] = event
	data["sequence_number"] = r.seq
	r.seq++
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}

func (r *responsesSSE) shell(status string) map[string]interface{} {
	return map[string]interface{}{
		"id": r.id, "object": "response", "created_at": r.created, "status": status, "model": r.model, "output": []interface{}{},
	}
}

func (r *responsesSSE) closeText(w *bufio.Writer) {
	if !r.textOpen {
		return
	}
	r.textOpen = false
	text := r.text.String()
	part := map[string]interface{}{"type": "output_text", "text": text, "annotations": []interface{}{}}
	r.send(w, "response.output_text.done", map[string]interface{}{"item_id": r.textID, "output_index": r.textIndex, "content_index": 0, "text": text})
	r.send(w, "response.content_part.done", map[string]interface{}{"item_id": r.textID, "output_index": r.textIndex, "content_index": 0, "part": part})
	r.send(w, "response.output_item.done", map[string]interface{}{"output_index": r.textIndex, "item": map[string]interface{}{
		"type": "message", "id": r.textID, "status": "completed", "role": "assistant", "content": []interface{}{part},
	}})
}

func (r *responsesSSE) callItem(c *responsesCall, status string) map[string]interface{} {
	return map[string]interface{}{"type": "function_call", "id": c.itemID, "call_id": c.callID, "name": c.name, "arguments": c.args.String(), "status": status}
}

func (r *responsesSSE) Line(w *bufio.Writer, line string) {
	data, ok := strings.CutPrefix(line, "data: ")
	if !ok {
		return
	}
	if data == "[DONE]" {
		r.closeText(w)
		var calls []map[string]interface{}
		for _, i := range r.order {
			c := r.calls[i]
			r.send(w, "response.function_call_arguments.done", map[string]interface{}{"item_id": c.itemID, "output_index": c.index, "arguments": c.args.String()})
			r.send(w, "response.output_item.done", map[string]interface{}{"output_index": c.index, "item": r.callItem(c, "completed")})
			calls = append(calls, r.callItem(c, "completed"))
		}
		resp := responseObject(r.id, r.model, r.created, r.text.String(), calls, r.finish, r.usage.in, r.usage.out, r.usage.cached)
		event := "response.completed"
		if resp["status"] == "incomplete" {
			event = "response.incomplete"
		}
		r.send(w, event, map[string]interface{}{"response": resp})
		return
	}
	var chunk struct {
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
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return
	}
	if !r.started {
		r.started = true
		r.id = newItemID("resp_")
		if chunk.Model != "" {
			r.model = chunk.Model
		}
		r.send(w, "response.created", map[string]interface{}{"response": r.shell("in_progress")})
		r.send(w, "response.in_progress", map[string]interface{}{"response": r.shell("in_progress")})
	}
	if chunk.Usage != nil {
		r.usage.in, r.usage.out, r.usage.cached = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.PromptTokensDetails.CachedTokens
	}
	for _, ch := range chunk.Choices {
		if t := ch.Delta.Content; t != nil && *t != "" {
			if !r.textOpen {
				r.textOpen, r.textID, r.textIndex = true, newItemID("msg_"), r.outIndex
				r.outIndex++
				r.send(w, "response.output_item.added", map[string]interface{}{"output_index": r.textIndex, "item": map[string]interface{}{
					"type": "message", "id": r.textID, "status": "in_progress", "role": "assistant", "content": []interface{}{},
				}})
				r.send(w, "response.content_part.added", map[string]interface{}{"item_id": r.textID, "output_index": r.textIndex, "content_index": 0,
					"part": map[string]interface{}{"type": "output_text", "text": "", "annotations": []interface{}{}}})
			}
			r.text.WriteString(*t)
			r.send(w, "response.output_text.delta", map[string]interface{}{"item_id": r.textID, "output_index": r.textIndex, "content_index": 0, "delta": *t})
		}
		for _, tc := range ch.Delta.ToolCalls {
			if r.calls == nil {
				r.calls = map[int]*responsesCall{}
			}
			c, seen := r.calls[tc.Index]
			if !seen {
				r.closeText(w)
				c = &responsesCall{itemID: newItemID("fc_"), callID: tc.ID, name: tc.Function.Name, index: r.outIndex}
				r.outIndex++
				r.calls[tc.Index] = c
				r.order = append(r.order, tc.Index)
				r.send(w, "response.output_item.added", map[string]interface{}{"output_index": c.index, "item": r.callItem(c, "in_progress")})
			}
			if tc.Function.Arguments != "" {
				c.args.WriteString(tc.Function.Arguments)
				r.send(w, "response.function_call_arguments.delta", map[string]interface{}{"item_id": c.itemID, "output_index": c.index, "delta": tc.Function.Arguments})
			}
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			r.finish = *ch.FinishReason
		}
	}
}
