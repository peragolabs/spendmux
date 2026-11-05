package proxy

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/models"
	"github.com/vrannang1/spendmux/oss/memory"
)

// memorySession carries one request's opt-in memory: who owns the thread, and
// the client's new messages to store once the provider has answered.
type memorySession struct {
	userID, keyID uuid.UUID
	token, thread string
	pending       []json.RawMessage
}

var errMemoryUnavailable = errors.New("memory unavailable")

// attachMemory prepends a thread's stored history to req when the client sent
// X-Spendmux-Memory. It rewrites req.Raw and req.Messages in place, so the
// spend reservation, loop detector and every fallback hop see the full
// conversation. It returns nil when the request did not opt in.
func attachMemory(c *fiber.Ctx, pk models.PlatformAPIKey, req *openAIChatRequest) (*memorySession, error) {
	thread := strings.TrimSpace(c.Get(memory.HeaderThread))
	if thread == "" {
		return nil, nil
	}
	if !memory.ValidThreadID(thread) {
		return nil, memory.ErrInvalidThread
	}
	token := bearerToken(c)
	history, err := memory.Load(pk.ID, token, thread)
	if err != nil {
		slog.Error("memory_load_failed", "error", err)
		return nil, errMemoryUnavailable
	}

	raw, pending, injected, err := mergeHistory(req.Raw, history, memory.ContextBudgetChars())
	if err != nil {
		return nil, err
	}
	req.Raw = raw
	var merged struct {
		Messages []openAIMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	req.Messages = merged.Messages
	ms := &memorySession{userID: pk.UserID, keyID: pk.ID, token: token, thread: thread, pending: pending}
	c.Set(memory.HeaderInjected, strconv.Itoa(injected))
	c.Locals("memory", ms)
	return ms, nil
}

// mergeHistory inserts the newest history that fits budget between the
// request's leading system messages and its new messages. The client's system
// prompt stays first, and system messages are never stored: the client
// resends its own each call. pending is what to store after a reply.
func mergeHistory(raw []byte, history []json.RawMessage, budget int) (out []byte, pending []json.RawMessage, injected int, err error) {
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, 0, err
	}
	var incoming []json.RawMessage
	if err = json.Unmarshal(payload["messages"], &incoming); err != nil {
		return nil, nil, 0, err
	}
	lead := 0
	for lead < len(incoming) && memory.Role(incoming[lead]) == "system" {
		lead++
	}
	for _, m := range incoming[lead:] {
		if memory.Role(m) != "system" {
			pending = append(pending, m)
		}
	}
	window := memory.Window(history, budget)
	combined := make([]json.RawMessage, 0, len(incoming)+len(window))
	combined = append(combined, incoming[:lead]...)
	combined = append(combined, window...)
	combined = append(combined, incoming[lead:]...)
	msgs, err := json.Marshal(combined)
	if err != nil {
		return nil, nil, 0, err
	}
	payload["messages"] = msgs
	out, err = json.Marshal(payload)
	return out, pending, len(window), err
}

// save stores the client's new messages and the provider's reply. A failure
// is logged, not returned: the caller already has its answer.
func (m *memorySession) save(reply json.RawMessage) {
	if m == nil || len(reply) == 0 {
		return
	}
	msgs := append(append([]json.RawMessage{}, m.pending...), reply)
	if err := memory.Append(m.userID, m.keyID, m.token, m.thread, msgs); err != nil {
		slog.Error("memory_save_failed", "error", err)
	}
}

// replyFromBody extracts choices[0].message from an OpenAI-shaped response.
func replyFromBody(body []byte) json.RawMessage {
	var r struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Choices) == 0 || len(r.Choices[0].Message) == 0 {
		return nil
	}
	return r.Choices[0].Message
}

// replyFromText builds the assistant message for a streamed reply.
func replyFromText(text string) json.RawMessage {
	if text == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"role": "assistant", "content": text})
	return b
}

func bearerToken(c *fiber.Ctx) string {
	h := c.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// V1DeleteMemory forgets one memory thread of the calling token:
// DELETE /v1/memory/:thread.
func V1DeleteMemory(c *fiber.Ctx) error {
	pk, ok := authenticatePlatformKey(c)
	if !ok {
		return nil
	}
	thread := c.Params("thread")
	if !memory.ValidThreadID(thread) {
		return c.Status(400).JSON(fiber.Map{"error": fiber.Map{"message": memory.ErrInvalidThread.Error(), "type": "invalid_request_error"}})
	}
	if err := memory.Delete(pk.ID, thread); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fiber.Map{"message": "could not delete memory", "type": "api_error"}})
	}
	return c.JSON(fiber.Map{"deleted": true, "thread": thread})
}
