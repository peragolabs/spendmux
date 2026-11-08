// Package playground is the BYOK chat playground: a JSON bootstrap endpoint
// for the React page and a WebSocket stream that reuses the engine's vault,
// provider adapters, fallback ladder and usage accounting.
package playground

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"

	"github.com/peragolabs/spendmux/internal/actor"
	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/internal/upstream"
	"github.com/peragolabs/spendmux/models"
	"github.com/peragolabs/spendmux/oss/keys"
	"github.com/peragolabs/spendmux/oss/proxy"
	"github.com/peragolabs/spendmux/oss/usage"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the payload from the playground WebSocket.
type ChatRequest struct {
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	Prompt         string        `json:"prompt"`
	ConversationID string        `json:"conversation_id"`
	History        []ChatMessage `json:"history"`
	Save           bool          `json:"save"`     // when false, nothing is written to the DB
	Fallback       bool          `json:"fallback"` // walk the /fallback ladder, same rules as /v1
}

func normalizeMathDelimiters(text string) string {
	text = strings.ReplaceAll(text, `\[`, "$$")
	text = strings.ReplaceAll(text, `\]`, "$$")
	text = strings.ReplaceAll(text, `\(`, "$")
	text = strings.ReplaceAll(text, `\)`, "$")
	mixedInline := regexp.MustCompile(`\$([^$\n]+?)\\[)\]]`)
	text = mixedInline.ReplaceAllString(text, `$$$1$`)
	mixedInline2 := regexp.MustCompile(`\\\(([^$\n]+?)\$`)
	text = mixedInline2.ReplaceAllString(text, `$$$1$`)
	return text
}

// Bootstrap returns what the playground page needs on load.
func Bootstrap(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	db := database.Get()
	var user models.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var keys []models.ProviderKey
	db.Where("user_id = ?", userID).Order("provider asc").Find(&keys)

	type providerOpt struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Suggested []string `json:"suggested"`
	}
	opts := make([]providerOpt, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k.Provider] { // several keys for one provider are one option
			continue
		}
		seen[k.Provider] = true
		name := k.Provider
		if p, ok := providers.Get(k.Provider); ok {
			name = p.Name
		}
		// Static suggestions only: the page must not block on four upstream
		// /models calls. The client fetches the live list for the selected
		// provider after paint (see Chat.svelte loadLiveModels).
		ids := providers.SuggestedModels(k.Provider)
		opts = append(opts, providerOpt{
			ID:        k.Provider,
			Name:      name,
			Suggested: ids,
		})
	}

	var recentConversations []models.Conversation
	db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(40).
		Find(&recentConversations)

	// Hide legacy privacy-mode shells (title only, no stored message text).
	filtered := make([]models.Conversation, 0, len(recentConversations))
	for _, conv := range recentConversations {
		var n int64
		db.Model(&models.Message{}).
			Where("conversation_id = ? AND content <> ''", conv.ID).
			Count(&n)
		if n > 0 {
			filtered = append(filtered, conv)
		}
		if len(filtered) >= 20 {
			break
		}
	}
	recentConversations = filtered

	var activeConversation *models.Conversation
	if convIDParam := c.Query("c"); convIDParam != "" {
		if convID, err := uuid.Parse(convIDParam); err == nil {
			var conv models.Conversation
			if db.Preload("Messages").
				Where("id = ? AND user_id = ?", convID, userID).
				First(&conv).Error == nil {
				activeConversation = &conv
			}
		}
	}

	type TemplateMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Model   string `json:"model"`
	}
	var initialMessages []TemplateMessage
	if activeConversation != nil {
		for _, m := range activeConversation.Messages {
			initialMessages = append(initialMessages, TemplateMessage{
				Role:    m.Role,
				Content: m.Content,
				Model:   m.Model,
			})
		}
	}

	activeID := ""
	activeTitle := ""
	if activeConversation != nil {
		activeID = activeConversation.ID.String()
		activeTitle = activeConversation.Title
	}

	return c.JSON(fiber.Map{
		"Title":                   "Playground",
		"User":                    user,
		"Providers":               opts,
		"RecentConversations":     recentConversations,
		"ActiveConversationID":    activeID,
		"ActiveConversationTitle": activeTitle,
		"InitialMessages":         initialMessages,
	})
}

// HandleChatStream streams a playground reply using the user's vault keys.
func HandleChatStream(c *websocket.Conn) {
	c.SetReadLimit(1 << 20)
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok || userIDStr == "" {
		_ = writeWS(c, []byte("⚠️ Not authenticated"))
		_ = c.Close()
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		_ = writeWS(c, []byte("⚠️ Invalid session"))
		_ = c.Close()
		return
	}
	defer func() { _ = c.Close() }()

	db := database.Get()

	_, msg, err := c.ReadMessage()
	if err != nil {
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		_ = writeWS(c, []byte("⚠️ Invalid request"))
		return
	}

	if strings.TrimSpace(req.Prompt) == "" {
		_ = writeWS(c, []byte("⚠️ Empty prompt"))
		return
	}
	const maxPromptChars = 8000
	if len(req.Prompt) > maxPromptChars {
		_ = writeWS(c, []byte(fmt.Sprintf(`{"error":"prompt_too_long","max":%d}`, maxPromptChars)))
		return
	}

	providerID, modelName, err := providers.ResolveRoute(req.Provider, req.Model)
	if err != nil {
		_ = writeWS(c, []byte("⚠️ "+err.Error()))
		return
	}
	if _, ok := providers.Get(providerID); !ok {
		_ = writeWS(c, []byte("⚠️ Unknown provider"))
		return
	}
	hops := playgroundHops(userID, playgroundHop{Provider: providerID, Model: modelName}, req.Fallback)
	if req.Fallback && len(hops) == 1 {
		_ = writeWS(c, []byte("⚠️ Fallback is on but the ladder has no usable hops beyond the primary — add one on /fallback"))
		return
	}

	routeLabel := providerID + "/" + modelName
	turnStart := time.Now()

	var conversation models.Conversation
	persist := req.Save

	if persist {
		if req.ConversationID != "" {
			convID, err := uuid.Parse(req.ConversationID)
			if err != nil {
				_ = writeWS(c, []byte("⚠️ Invalid conversation ID"))
				return
			}
			if err := db.Where("id = ? AND user_id = ?", convID, userID).First(&conversation).Error; err != nil {
				_ = writeWS(c, []byte("⚠️ Conversation not found"))
				return
			}
		} else {
			title := req.Prompt
			if len(title) > 48 {
				title = title[:48] + "…"
			}
			conversation = models.Conversation{UserID: userID, Title: title}
			if err := db.Create(&conversation).Error; err != nil {
				_ = writeWS(c, []byte("⚠️ Could not create conversation"))
				return
			}
			idMsg, _ := json.Marshal(map[string]string{"conversation_id": conversation.ID.String()})
			if err := writeWS(c, idMsg); err != nil {
				return
			}
		}

		if err := db.Create(&models.Message{
			ConversationID: conversation.ID,
			UserID:         userID,
			Role:           "user",
			Content:        req.Prompt,
			Model:          routeLabel,
			Cost:           0,
		}).Error; err != nil {
			log.Printf("playground save user msg: %v", err)
		}
	}

	chatMessages := []map[string]string{
		{
			"role": "system",
			"content": `You are a helpful AI assistant. Answer clearly and accurately.
Format ALL math using LaTeX. Use $$ for display/block equations and $ for inline math.
Use markdown for headings, lists, and code blocks.`,
		},
	}
	for _, m := range req.History {
		role := m.Role
		if role == "ai" {
			role = "assistant"
		}
		chatMessages = append(chatMessages, map[string]string{"role": role, "content": m.Content})
	}
	chatMessages = append(chatMessages, map[string]string{"role": "user", "content": req.Prompt})

	var fullResponse string
	var usage playgroundUsage
	var lastErr error
	answered := false
	for i, hop := range hops {
		cfg, ok := providers.Get(hop.Provider)
		if !ok {
			continue
		}
		apiKey, err := proxy.ResolveAPIKey(userID, &cfg)
		if err != nil {
			lastErr = err
			if i == 0 && !req.Fallback {
				break
			}
			continue // no key for this hop — skip it, like /v1 does
		}
		if i > 0 {
			note, _ := json.Marshal(map[string]string{
				"type": "fallback", "to": hop.Provider + "/" + hop.Model, "reason": lastErr.Error(),
			})
			_ = writeWS(c, note)
		}
		start := time.Now()
		usage = playgroundUsage{}
		if cfg.NativeAnthropic {
			fullResponse, usage, err = playgroundAnthropic(c, cfg, apiKey, hop.Model, chatMessages)
		} else {
			fullResponse, usage, err = playgroundOpenAIStream(c, cfg, apiKey, hop.Model, chatMessages)
		}
		latencyMs := time.Since(start).Milliseconds()
		if err != nil {
			lastErr = err
			log.Printf("playground error user=%s hop=%s/%s: %v", userID, hop.Provider, hop.Model, err)
			logPlaygroundUsage(userID, hop.Provider, hop.Model, usage, latencyMs, upstreamStatus(err), err.Error())
			if req.Fallback && retryableUpstream(err) && i < len(hops)-1 {
				continue
			}
			break
		}
		if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
			usage.PromptTokens, usage.CompletionTokens = estimateTokens(chatMessages, fullResponse)
		}
		logPlaygroundUsage(userID, hop.Provider, hop.Model, usage, latencyMs, http.StatusOK, "")
		routeLabel = hop.Provider + "/" + hop.Model
		answered = true
		break
	}
	if !answered {
		if lastErr == nil {
			lastErr = fmt.Errorf("no usable provider")
		}
		_ = writeWS(c, []byte("⚠️ "+lastErr.Error()))
		return
	}

	fullResponse = strings.TrimSpace(normalizeMathDelimiters(fullResponse))
	if fullResponse != "" {
		replaceMsg, _ := json.Marshal(map[string]string{"type": "replace", "content": fullResponse})
		if err := writeWS(c, replaceMsg); err != nil {
			return
		}
		if persist {
			if err := db.Create(&models.Message{
				ConversationID: conversation.ID,
				UserID:         userID,
				Role:           "assistant",
				Content:        fullResponse,
				Model:          routeLabel,
				Cost:           0,
			}).Error; err != nil {
				log.Printf("playground save assistant msg: %v", err)
			}
		}
	}

	log.Printf("playground ok user=%s %s save=%v fallback=%v latency=%s", userID, routeLabel, persist, req.Fallback, time.Since(turnStart).Round(time.Millisecond))
}

func writeWS(c *websocket.Conn, payload []byte) error {
	return c.WriteMessage(websocket.TextMessage, payload)
}

// upstreamError carries the provider's HTTP status so the fallback loop can
// tell "try the next hop" (429/5xx, transport) from "the request is wrong" (4xx).
type upstreamError struct {
	status int
	msg    string
}

func (e *upstreamError) Error() string { return e.msg }

func upstreamStatus(err error) int {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return ue.status
	}
	return http.StatusBadGateway
}

func retryableUpstream(err error) bool {
	var ue *upstreamError
	if !errors.As(err, &ue) {
		return true // transport-level failure
	}
	return ue.status == 429 || ue.status == 502 || ue.status == 503 || ue.status == 504
}

// playgroundHop is one provider/model to try.
type playgroundHop struct {
	Provider string
	Model    string
}

// playgroundHops builds primary + ladder, skipping hops equal to the primary
// or without a saved key — the same filtering /v1 applies.
func playgroundHops(userID uuid.UUID, primary playgroundHop, useFallback bool) []playgroundHop {
	hops := []playgroundHop{primary}
	if !useFallback {
		return hops
	}
	steps, err := keys.LoadFallbackSteps(userID)
	if err != nil {
		return hops
	}
	for _, st := range steps {
		pid, m, err := providers.ResolveRoute(st.Provider, st.Model)
		if err != nil || (pid == primary.Provider && m == primary.Model) {
			continue
		}
		hops = append(hops, playgroundHop{Provider: pid, Model: m})
	}
	return hops
}

// playgroundUsage is what a provider reported (or, if it reported nothing, a
// character-based estimate) so the playground shows up in the spend observatory
// exactly like /v1 traffic does.
type playgroundUsage struct {
	PromptTokens     int
	CompletionTokens int
	TTFTMs           int64
}

// logPlaygroundUsage records one playground turn as a UsageEvent. BYOK spend
// always counts at list price, same as the proxy.
func logPlaygroundUsage(userID uuid.UUID, provider, model string, u playgroundUsage, latencyMs int64, status int, errMsg string) {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	cost := providers.EstimateCostUSDMicros(provider, model, u.PromptTokens, u.CompletionTokens)
	ev := models.UsageEvent{
		UserID:            userID,
		Provider:          provider,
		Model:             model,
		Endpoint:          "playground",
		PromptTokens:      u.PromptTokens,
		CompletionTokens:  u.CompletionTokens,
		TotalTokens:       u.PromptTokens + u.CompletionTokens,
		CostUSDMicros:     cost,
		ListCostUSDMicros: cost,
		LatencyMs:         latencyMs,
		TTFTMs:            u.TTFTMs,
		StatusCode:        status,
		Streamed:          true,
		ErrorMessage:      errMsg,
		SessionID:         "playground",
	}
	usage.ApplyHistoryPolicy(&ev)
	actor.LogUsageEvent(ev)
}

// estimateTokens is the fallback when a provider omits usage: ~4 chars/token.
func estimateTokens(messages []map[string]string, completion string) (prompt, out int) {
	for _, m := range messages {
		prompt += len(m["content"])
	}
	return prompt/4 + 1, len(completion)/4 + 1
}

func playgroundOpenAIStream(c *websocket.Conn, cfg providers.Config, apiKey, modelName string, messages []map[string]string) (string, playgroundUsage, error) {
	var usage playgroundUsage
	payload := map[string]interface{}{
		"model":      modelName,
		"messages":   messages,
		"stream":     true,
		"max_tokens": 4096,
		// Ask for a trailing usage chunk (OpenAI, Groq, and most compatible APIs).
		"stream_options": map[string]bool{"include_usage": true},
	}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", usage, fmt.Errorf("bad request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := proxy.SetUpstreamAuth(httpReq, cfg, apiKey, body); err != nil {
		return "", usage, err
	}

	start := time.Now()
	resp, err := upstream.Do(httpReq)
	if err != nil {
		return "", usage, fmt.Errorf("%s", upstream.DescribeError(cfg.ID, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", usage, &upstreamError{status: resp.StatusCode, msg: upstream.HumanizeError(cfg.ID, resp.StatusCode, errBody)}
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line[6:]), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if usage.TTFTMs == 0 {
				usage.TTFTMs = time.Since(start).Milliseconds()
			}
			token := chunk.Choices[0].Delta.Content
			sb.WriteString(token)
			_ = c.WriteMessage(websocket.TextMessage, []byte(token))
		}
	}
	return sb.String(), usage, nil
}

func playgroundAnthropic(c *websocket.Conn, cfg providers.Config, apiKey, modelName string, messages []map[string]string) (string, playgroundUsage, error) {
	var usage playgroundUsage
	system := ""
	var msgs []map[string]string
	for _, m := range messages {
		if m["role"] == "system" {
			if system != "" {
				system += "\n"
			}
			system += m["content"]
			continue
		}
		msgs = append(msgs, map[string]string{"role": m["role"], "content": m["content"]})
	}
	payload := map[string]interface{}{
		"model":      modelName,
		"max_tokens": 4096,
		"messages":   msgs,
	}
	if system != "" {
		payload["system"] = system
	}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(cfg.BaseURL, "/") + cfg.ChatPath
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", usage, fmt.Errorf("bad request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := proxy.SetUpstreamAuth(httpReq, cfg, apiKey, body); err != nil {
		return "", usage, err
	}

	start := time.Now()
	resp, err := upstream.Do(httpReq)
	if err != nil {
		return "", usage, fmt.Errorf("%s", upstream.DescribeError(cfg.ID, err))
	}
	defer resp.Body.Close()
	respBody, err := upstream.ReadAllLimit(resp.Body, 8<<20)
	if err != nil {
		return "", usage, fmt.Errorf("upstream response too large or unreadable")
	}
	usage.TTFTMs = time.Since(start).Milliseconds() // non-streaming: first byte == whole body
	if resp.StatusCode != http.StatusOK {
		return "", usage, &upstreamError{status: resp.StatusCode, msg: upstream.HumanizeError(cfg.ID, resp.StatusCode, respBody)}
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", usage, fmt.Errorf("bad anthropic response")
	}
	usage.PromptTokens = parsed.Usage.InputTokens
	usage.CompletionTokens = parsed.Usage.OutputTokens
	var sb strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" && block.Text != "" {
			sb.WriteString(block.Text)
			_ = c.WriteMessage(websocket.TextMessage, []byte(block.Text))
		}
	}
	return sb.String(), usage, nil
}
