package api

import (
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/seaturt/server/internal/agent"
	"github.com/gin-gonic/gin"
)

// SessionHandler handles Session management API endpoints.
type SessionHandler struct {
	mgr *agent.Manager
}

// NewSessionHandler creates a new SessionHandler.
func NewSessionHandler(mgr *agent.Manager) *SessionHandler {
	return &SessionHandler{mgr: mgr}
}

// CreateSessionRequest is the request body for POST /api/agents/:id/sessions.
type CreateSessionRequest struct {
	Title string `json:"title"`
}

// ListSessions handles GET /api/agents/:id/sessions
func (h *SessionHandler) ListSessions(c *gin.Context) {
	agentID := c.Param("id")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	sessions, err := h.mgr.GetStore().ListSessions(agentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if sessions == nil {
		sessions = []*agent.Session{}
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// CreateSession handles POST /api/agents/:id/sessions
func (h *SessionHandler) CreateSession(c *gin.Context) {
	agentID := c.Param("id")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body — title defaults to "新对话"
		req = CreateSessionRequest{}
	}

	title := req.Title
	if title == "" {
		title = "新对话"
	}

	now := time.Now()
	sess := &agent.Session{
		ID:        fmt.Sprintf("sess_%d", now.UnixNano()),
		AgentID:   agentID,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.mgr.GetStore().CreateSession(sess); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, sess)
}

// UpdateSessionRequest is the request body for PUT /api/agents/:id/sessions/:sid.
type UpdateSessionRequest struct {
	Title string `json:"title" binding:"required"`
}

// UpdateSession handles PUT /api/agents/:id/sessions/:sid
func (h *SessionHandler) UpdateSession(c *gin.Context) {
	agentID := c.Param("id")
	sessionID := c.Param("sid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	sess, err := h.mgr.GetStore().GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if sess.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	var req UpdateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sess.Title = req.Title
	sess.UpdatedAt = time.Now()

	if err := h.mgr.GetStore().UpdateSession(sess); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, sess)
}

// DeleteSession handles DELETE /api/agents/:id/sessions/:sid
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	agentID := c.Param("id")
	sessionID := c.Param("sid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	sess, err := h.mgr.GetStore().GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if sess.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if err := h.mgr.GetStore().DeleteSession(sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// truncateTitle truncates a string to maxRunes runes.
func truncateTitle(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}
