package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/gin-gonic/gin"
)

// EventHandler handles SSE event subscription endpoints (v0.3.1).
type EventHandler struct {
	mgr *agent.Manager
}

// NewEventHandler creates a new EventHandler.
func NewEventHandler(mgr *agent.Manager) *EventHandler {
	return &EventHandler{mgr: mgr}
}

// GlobalEvents handles GET /api/events — global SSE for agent-level events.
// The connection stays open as long as the client is connected.
// Events: session_created, session_updated, session_deleted, cron_execution_started, cron_execution_finished
func (h *EventHandler) GlobalEvents(c *gin.Context) {
	// SSE headers
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

	hub := h.mgr.GetEventHub()
	subscriberID := fmt.Sprintf("global_%d", time.Now().UnixNano())
	ch := hub.Global().Subscribe(subscriberID)

	slog.Info("global SSE subscriber connected", "id", subscriberID)

	// Send initial heartbeat to confirm connection
	slog.Info("[GlobalEvents] sending connected heartbeat", "id", subscriberID)
	fmt.Fprintf(c.Writer, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()

	// Stream events until client disconnects
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			hub.Global().Unsubscribe(subscriberID)
			slog.Info("global SSE subscriber disconnected", "id", subscriberID)
			return
		case event, ok := <-ch:
			if !ok {
				slog.Info("[GlobalEvents] channel closed", "id", subscriberID)
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				slog.Warn("[GlobalEvents] marshal error", "id", subscriberID, "err", err)
				continue
			}
			slog.Info("[GlobalEvents] sending event to client", "id", subscriberID, "event_type", event.Type, "data", string(data))
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// SessionEvents handles GET /api/agents/:id/sessions/:sid/events — session-level SSE.
// On connect: sends snapshot (current turn's accumulated events), then incremental events.
func (h *EventHandler) SessionEvents(c *gin.Context) {
	agentID := c.Param("id")
	sessionID := c.Param("sid")

	// Verify agent exists
	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	// Verify session exists
	store := h.mgr.GetStore()
	sess, err := store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if sess.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// SSE headers
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

	hub := h.mgr.GetEventHub()
	sessionBus := hub.GetOrCreateSessionBus(sessionID)
	subscriberID := fmt.Sprintf("sess_%s_%d", sessionID, time.Now().UnixNano())

	// Subscribe BEFORE reading snapshot to avoid missing events between snapshot and subscribe
	ch := sessionBus.Subscribe(subscriberID)

	slog.Info("session SSE subscriber connected",
		"id", subscriberID,
		"session_id", sessionID,
	)

	// Send snapshot first (catch up on current turn's accumulated events)
	snapshot := sessionBus.Snapshot()
	if len(snapshot) > 0 {
		// Wrap snapshot events in a "snapshot" envelope
		snapshotData, err := json.Marshal(map[string]any{
			"type":   "snapshot",
			"events": snapshot,
		})
		if err == nil {
			fmt.Fprintf(c.Writer, "data: %s\n\n", snapshotData)
			flusher.Flush()
		}
	}

	// Send connected confirmation
	fmt.Fprintf(c.Writer, "data: {\"type\":\"connected\",\"session_id\":\"%s\"}\n\n", sessionID)
	flusher.Flush()

	// Stream events until client disconnects
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			sessionBus.Unsubscribe(subscriberID)
			slog.Info("session SSE subscriber disconnected",
				"id", subscriberID,
				"session_id", sessionID,
			)
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
