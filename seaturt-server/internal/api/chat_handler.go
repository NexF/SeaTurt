package api

import (
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
}

// allowedImageTypes lists accepted image MIME types.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// Chat handles POST /api/agents/:id/chat — sends a message and streams the response via SSE.
func (h *ChatHandler) Chat(c *gin.Context) {
	id := c.Param("id")

	// Parse content from either JSON or multipart
	content, err := h.parseContent(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(content) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
		return
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

	// Validate content types against model capabilities
	endpoint, err := h.mgr.GetConfig().ResolveLLM("", ag.Config.Model)
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

	store := h.mgr.GetStore()

	// Save user message
	userMsg := &agent.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		AgentID:   id,
		Role:      "user",
		Content:   llm.Content(content),
		CreatedAt: time.Now(),
	}
	if err := store.CreateMessage(userMsg); err != nil {
		slog.Error("failed to save user message", "err", err)
	}

	// Load conversation history from DB and convert to LLM format
	dbMessages, err := store.ListMessages(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load history"})
		return
	}

	history := convertToLLMMessages(dbMessages)

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	// Run agent loop with SSE streaming
	loopCfg := agent.LoopConfig{
		LLMClient:    h.mgr.GetLLMClient(),
		Router:       router,
		SystemPrompt: h.mgr.LoadSystemPrompt(ag),
	}

	finalContent, loopMessages, loopErr := agent.RunLoop(loopCfg, history, func(event agent.StreamEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	})

	if loopErr != nil {
		slog.Error("agent loop error", "agent_id", id, "err", loopErr)
		errEvent, _ := json.Marshal(agent.StreamEvent{
			Type: "error",
			Data: map[string]string{"message": loopErr.Error()},
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", errEvent)
		flusher.Flush()
	}

	// Save new messages produced by the loop.
	// loopMessages = [system(injected)] + history + new messages.
	// Skip system messages and the original history (len(history) non-system msgs).
	historyLen := len(history)
	skipped := 0
	for _, lm := range loopMessages {
		if lm.Role == "system" {
			continue
		}
		if skipped < historyLen {
			skipped++
			continue
		}
		msg := &agent.Message{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			AgentID:   id,
			Role:      lm.Role,
			Content:   lm.Content,
			CreatedAt: time.Now(),
		}
		// Serialize tool_calls if present
		if len(lm.ToolCalls) > 0 {
			if tcJSON, err := json.Marshal(lm.ToolCalls); err == nil {
				msg.ToolCalls = string(tcJSON)
			}
		}
		// Restore tool_call_id for tool messages
		if lm.ToolCallID != "" {
			msg.ToolCallID = lm.ToolCallID
		}
		if err := store.CreateMessage(msg); err != nil {
			slog.Error("failed to save loop message", "role", lm.Role, "err", err)
		}
	}
	_ = finalContent
}

// parseContent extracts content blocks from the request.
// Supports both application/json and multipart/form-data.
func (h *ChatHandler) parseContent(c *gin.Context) ([]llm.ContentBlock, error) {
	contentType := c.ContentType()

	if strings.HasPrefix(contentType, "multipart/form-data") {
		return h.parseMultipartContent(c)
	}

	// Default: JSON
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	return req.Content, nil
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

// GetHistory handles GET /api/agents/:id/history
func (h *ChatHandler) GetHistory(c *gin.Context) {
	id := c.Param("id")

	// Verify agent exists
	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	messages, err := h.mgr.GetStore().ListMessages(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if messages == nil {
		messages = []*agent.Message{}
	}

	c.JSON(http.StatusOK, messages)
}

// DeleteHistory handles DELETE /api/agents/:id/history
func (h *ChatHandler) DeleteHistory(c *gin.Context) {
	id := c.Param("id")

	// Verify agent exists
	if _, err := h.mgr.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	if err := h.mgr.GetStore().DeleteMessages(id); err != nil {
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
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
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
