package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/gin-gonic/gin"
)

// ChatHandler handles conversation API endpoints.
type ChatHandler struct {
	mgr          *agent.Manager
	maxImageSize int // bytes, 0 means no limit
}

// NewChatHandler creates a new ChatHandler.
func NewChatHandler(mgr *agent.Manager, maxImageSize int) *ChatHandler {
	return &ChatHandler{mgr: mgr, maxImageSize: maxImageSize}
}

// ChatRequest is the request body for POST /api/agents/:id/chat (JSON mode).
type ChatRequest struct {
	Content []llm.ContentBlock `json:"content" binding:"required"`
	TurnID  string             `json:"turn_id"` // frontend-generated turn ID (v0.3.3)
}

// allowedImageTypes lists accepted image MIME types.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// ChatResponse is the JSON response for POST /api/agents/:id/sessions/:sid/chat.
type ChatResponse struct {
	TurnID    string `json:"turn_id"`
	MessageID string `json:"message_id"`
}

// Chat handles POST /api/agents/:id/sessions/:sid/chat — accepts a message and kicks off
// the agent loop in a background goroutine. All streaming events are delivered exclusively
// through the session SSE channel (GET /api/agents/:id/sessions/:sid/events).
// Returns immediately with {turn_id, message_id}.
func (h *ChatHandler) Chat(c *gin.Context) {
	id := c.Param("id")
	sessionID := c.Param("sid")

	// Parse content and turn_id from either JSON or multipart
	content, turnID, err := h.parseContentAndTurnID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(content) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
		return
	}

	// Auto-generate turn_id if not provided by frontend
	if turnID == "" {
		turnID = fmt.Sprintf("turn_%d", time.Now().UnixNano())
	}

	// Validate image blocks
	if err := h.validateImages(content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify agent exists and is running
	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	if ag.Status != agent.StatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("agent is %s, not running", ag.Status)})
		return
	}

	// Verify session exists and belongs to this agent
	store := h.mgr.GetStore()
	sess, err := store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if sess.AgentID != id {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// Validate content types against model capabilities
	endpoint, err := h.mgr.GetConfig().ResolveLLM(ag.Config.Provider, ag.Config.Model)
	if err == nil {
		if err := llm.ValidateContent(endpoint.Model, endpoint.Input, content); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	router := h.mgr.GetRouter(id)
	if router == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent MCP router not available"})
		return
	}

	// Check if this is the first user message for auto-title
	isFirstMessage := sess.Title == "新对话"

	// Save user message
	userMsg := &agent.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		AgentID:   id,
		SessionID: sessionID,
		Role:      "user",
		Content:   llm.Content(content),
		CreatedAt: time.Now(),
	}
	if err := store.CreateMessage(userMsg); err != nil {
		slog.Error("failed to save user message", "err", err)
	}

	// Publish user_message event to SessionBus
	sessionBus := h.mgr.GetEventHub().GetOrCreateSessionBus(sessionID)
	sessionBus.Publish(agent.StreamEvent{
		Type:   "user_message",
		TurnID: turnID,
		Data: agent.UserMessageEvent{
			TurnID:  turnID,
			ID:      userMsg.ID,
			Content: userMsg.Content,
		},
	})

	// Auto-generate session title from first user message
	if isFirstMessage {
		userText := llm.Content(content).String()
		if userText != "" {
			newTitle := truncateTitle(userText, 20)
			sess.Title = newTitle
			sess.UpdatedAt = time.Now()
			if err := store.UpdateSession(sess); err != nil {
				slog.Warn("failed to auto-update session title", "err", err)
			} else {
				// Publish session_updated via SessionBus so SSE subscribers see the title change
				sessionBus.Publish(agent.StreamEvent{
					Type:   "session_updated",
					TurnID: turnID,
					Data: map[string]string{
						"session_id": sessionID,
						"title":      sess.Title,
					},
				})
			}
		}
	}

	// Load conversation history from DB (session-level) and convert to LLM format
	dbMessages, err := store.ListMessagesBySession(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load history"})
		return
	}

	history := convertToLLMMessages(dbMessages)

	// Cancel any previous active session for this session
	h.mgr.CancelActiveSession(sessionID)

	// Create a cancellable context (NOT derived from HTTP request — the goroutine outlives the request).
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, agent.AgentIDContextKey, id)

	// Register this session's cancel func
	h.mgr.SetActiveSession(sessionID, cancel)

	// Build loop config
	loopCfg := agent.LoopConfig{
		LLMClient:    h.mgr.GetLLMClientForAgent(ag),
		Router:       router,
		SystemPrompt: h.mgr.LoadSystemPrompt(ag),
		OnMessage: func(msg llm.ChatMessage) {
			dbMsg := &agent.Message{
				ID:               fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				AgentID:          id,
				SessionID:        sessionID,
				Role:             msg.Role,
				Content:          msg.Content,
				ReasoningContent: msg.ReasoningContent,
				CreatedAt:        time.Now(),
			}
			if len(msg.ToolCalls) > 0 {
				if tcJSON, err := json.Marshal(msg.ToolCalls); err == nil {
					dbMsg.ToolCalls = string(tcJSON)
				}
			}
			if msg.ToolCallID != "" {
				dbMsg.ToolCallID = msg.ToolCallID
			}
			if err := store.CreateMessage(dbMsg); err != nil {
				slog.Error("failed to save loop message", "role", msg.Role, "err", err)
			}
		},
		OnToolCallStart: func(toolCallID string, toolCancel context.CancelFunc) {
			h.mgr.SetActiveToolCall(sessionID, toolCallID, toolCancel)
		},
		OnToolCallEnd: func(toolCallID string) {
			h.mgr.ClearActiveToolCall(sessionID, toolCallID)
		},
	}

	// Launch agent loop in background goroutine — all events go through SessionBus only.
	go func() {
		defer h.mgr.ClearActiveSession(sessionID)
		defer cancel()

		_, _, loopErr := agent.RunLoop(ctx, loopCfg, history, func(event agent.StreamEvent) {
			sessionBus.Publish(event)
		}, turnID)

		// Update session updated_at after chat
		sess.UpdatedAt = time.Now()
		_ = store.UpdateSession(sess)

		if loopErr != nil {
			slog.Error("agent loop error", "agent_id", id, "session_id", sessionID, "err", loopErr)
			// Error event is already emitted by RunLoop via emit("error", ...) or we push one
			// for non-RunLoop errors (shouldn't happen, but be safe).
		}
	}()

	// Return immediately with turn_id and message_id
	c.JSON(http.StatusOK, ChatResponse{
		TurnID:    turnID,
		MessageID: userMsg.ID,
	})
}

// CancelChat handles POST /api/agents/:id/sessions/:sid/chat/cancel — cancels the entire active chat session.
func (h *ChatHandler) CancelChat(c *gin.Context) {
	id := c.Param("id")
	sessionID := c.Param("sid")

	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	cancelled := h.mgr.CancelActiveSession(sessionID)
	if !cancelled {
		c.JSON(http.StatusOK, gin.H{"status": "no_active_session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// CancelToolCall handles POST /api/agents/:id/sessions/:sid/chat/cancel-tool/:toolCallId
func (h *ChatHandler) CancelToolCall(c *gin.Context) {
	id := c.Param("id")
	sessionID := c.Param("sid")
	toolCallID := c.Param("toolCallId")

	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	cancelled := h.mgr.CancelActiveToolCall(sessionID, toolCallID)
	if !cancelled {
		c.JSON(http.StatusOK, gin.H{"status": "tool_call_not_found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// parseContentAndTurnID extracts content blocks and turn_id from the request.
// Supports both application/json and multipart/form-data.
func (h *ChatHandler) parseContentAndTurnID(c *gin.Context) ([]llm.ContentBlock, string, error) {
	contentType := c.ContentType()

	if strings.HasPrefix(contentType, "multipart/form-data") {
		blocks, err := h.parseMultipartContent(c)
		turnID := c.PostForm("turn_id")
		return blocks, turnID, err
	}

	// Default: JSON
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, "", fmt.Errorf("invalid request: %w", err)
	}
	return req.Content, req.TurnID, nil
}

// parseMultipartContent parses multipart/form-data with text and image fields.
func (h *ChatHandler) parseMultipartContent(c *gin.Context) ([]llm.ContentBlock, error) {
	var blocks []llm.ContentBlock

	// Parse text field
	if text := c.PostForm("text"); text != "" {
		blocks = append(blocks, llm.NewTextContent(text))
	}

	// Parse image file upload
	file, header, err := c.Request.FormFile("image")
	if err == nil {
		defer file.Close()

		// Check size
		if h.maxImageSize > 0 && header.Size > int64(h.maxImageSize) {
			return nil, fmt.Errorf("image size %d exceeds limit %d bytes", header.Size, h.maxImageSize)
		}

		// Read and encode
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, fmt.Errorf("read image: %w", err)
		}

		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = http.DetectContentType(data)
		}

		if !allowedImageTypes[mimeType] {
			return nil, fmt.Errorf("unsupported image type: %s (allowed: jpeg, png, gif, webp)", mimeType)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		blocks = append(blocks, llm.NewImageContent(encoded, mimeType))
	}

	return blocks, nil
}

// validateImages checks all image blocks for size and format compliance.
func (h *ChatHandler) validateImages(blocks []llm.ContentBlock) error {
	for _, b := range blocks {
		if b.Type != "image" || b.Image == nil {
			continue
		}

		// Validate mime type
		if !allowedImageTypes[b.Image.MimeType] {
			return fmt.Errorf("unsupported image type: %s (allowed: jpeg, png, gif, webp)", b.Image.MimeType)
		}

		// Validate size (base64 encoded size ≈ 4/3 * raw size)
		if h.maxImageSize > 0 {
			rawSize := len(b.Image.Data) * 3 / 4
			if rawSize > h.maxImageSize {
				return fmt.Errorf("image size ~%d exceeds limit %d bytes", rawSize, h.maxImageSize)
			}
		}
	}
	return nil
}

// GetHistory handles GET /api/agents/:id/sessions/:sid/history
func (h *ChatHandler) GetHistory(c *gin.Context) {
	id := c.Param("id")
	sessionID := c.Param("sid")

	// Verify agent exists
	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	messages, err := h.mgr.GetStore().ListMessagesBySession(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if messages == nil {
		messages = []*agent.Message{}
	}

	c.JSON(http.StatusOK, messages)
}

// DeleteHistory handles DELETE /api/agents/:id/sessions/:sid/history
func (h *ChatHandler) DeleteHistory(c *gin.Context) {
	id := c.Param("id")
	sessionID := c.Param("sid")

	// Verify agent exists
	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	if err := h.mgr.GetStore().DeleteMessagesBySession(sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// convertToLLMMessages converts stored messages to LLM ChatMessage format.
func convertToLLMMessages(msgs []*agent.Message) []llm.ChatMessage {
	result := make([]llm.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		cm := llm.ChatMessage{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCallID:       m.ToolCallID,
		}
		// Restore tool_calls if present
		if m.ToolCalls != "" {
			var tcs []llm.ToolCall
			if err := json.Unmarshal([]byte(m.ToolCalls), &tcs); err == nil {
				cm.ToolCalls = tcs
			}
		}
		result = append(result, cm)
	}
	return result
}
