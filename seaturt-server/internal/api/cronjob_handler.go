package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaturt/server/internal/agent"
	cronpkg "github.com/seaturt/server/internal/cron"
)

// CronJobHandler handles CronJob management API endpoints.
type CronJobHandler struct {
	mgr       *agent.Manager
	scheduler *cronpkg.Scheduler
}

// NewCronJobHandler creates a new CronJobHandler.
func NewCronJobHandler(mgr *agent.Manager, scheduler *cronpkg.Scheduler) *CronJobHandler {
	return &CronJobHandler{mgr: mgr, scheduler: scheduler}
}

// CreateCronJobRequest is the request body for POST /api/agents/:id/cron-jobs.
type CreateCronJobRequest struct {
	Type            string  `json:"type" binding:"required,oneof=cron at"`
	CronExpr        string  `json:"cron_expr"`
	RunAt           *string `json:"run_at"`
	Prompt          string  `json:"prompt" binding:"required"`
	SessionStrategy string  `json:"session_strategy"`
	SessionID       string  `json:"session_id"`
}

// UpdateCronJobRequest is the request body for PUT /api/agents/:id/cron-jobs/:jid.
type UpdateCronJobRequest struct {
	Type            *string `json:"type"`
	CronExpr        *string `json:"cron_expr"`
	RunAt           *string `json:"run_at"`
	Prompt          *string `json:"prompt"`
	SessionStrategy *string `json:"session_strategy"`
	SessionID       *string `json:"session_id"`
	Enabled         *bool   `json:"enabled"`
}

// ListCronJobs handles GET /api/agents/:id/cron-jobs
func (h *CronJobHandler) ListCronJobs(c *gin.Context) {
	agentID := c.Param("id")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	jobs, err := h.mgr.GetStore().ListCronJobs(agentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if jobs == nil {
		jobs = []*agent.CronJob{}
	}

	c.JSON(http.StatusOK, gin.H{"cron_jobs": jobs})
}

// CreateCronJob handles POST /api/agents/:id/cron-jobs
func (h *CronJobHandler) CreateCronJob(c *gin.Context) {
	agentID := c.Param("id")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	var req CreateCronJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate type-specific fields
	switch req.Type {
	case "cron":
		if req.CronExpr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cron_expr is required for type 'cron'"})
			return
		}
		if err := cronpkg.ValidateCronExpr(req.CronExpr); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid cron expression: %v", err)})
			return
		}
	case "at":
		if req.RunAt == nil || *req.RunAt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "run_at is required for type 'at'"})
			return
		}
		t, err := time.Parse(time.RFC3339, *req.RunAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid run_at (must be RFC 3339): %v", err)})
			return
		}
		if t.Before(time.Now()) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "run_at must be in the future"})
			return
		}
	}

	// Default session strategy
	sessionStrategy := req.SessionStrategy
	if sessionStrategy == "" {
		sessionStrategy = "fixed"
	}

	now := time.Now()
	job := &agent.CronJob{
		ID:              fmt.Sprintf("cron_%d", now.UnixNano()),
		AgentID:         agentID,
		Type:            req.Type,
		CronExpr:        req.CronExpr,
		Prompt:          req.Prompt,
		SessionStrategy: sessionStrategy,
		SessionID:       req.SessionID,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Set RunAt for at-type
	if req.Type == "at" && req.RunAt != nil {
		t, _ := time.Parse(time.RFC3339, *req.RunAt)
		job.RunAt = &t
	}

	// If session strategy is "fixed" and no session provided, create one
	if sessionStrategy == "fixed" && job.SessionID == "" {
		sess := &agent.Session{
			ID:        fmt.Sprintf("sess_%d", now.UnixNano()),
			AgentID:   agentID,
			Title:     fmt.Sprintf("定时任务: %s", truncateCronTitle(req.Prompt, 20)),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := h.mgr.GetStore().CreateSession(sess); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create session: %v", err)})
			return
		}
		job.SessionID = sess.ID

		// Broadcast session_created via GlobalBus so frontend refreshes session list
		h.mgr.GetEventHub().Global().Publish(agent.AgentEvent{
			Type:    "session_created",
			AgentID: agentID,
			Data: map[string]string{
				"session_id": sess.ID,
				"title":      sess.Title,
			},
		})
	}

	if err := h.mgr.GetStore().CreateCronJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Register with scheduler
	if h.scheduler != nil {
		if err := h.scheduler.RegisterJob(job); err != nil {
			// Non-fatal: job is persisted, scheduler registration failed
			c.JSON(http.StatusCreated, gin.H{"cron_job": job, "warning": fmt.Sprintf("scheduler registration failed: %v", err)})
			return
		}
	}

	c.JSON(http.StatusCreated, job)
}

// GetCronJob handles GET /api/agents/:id/cron-jobs/:jid
func (h *CronJobHandler) GetCronJob(c *gin.Context) {
	agentID := c.Param("id")
	jobID := c.Param("jid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	job, err := h.mgr.GetStore().GetCronJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	if job.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

// UpdateCronJob handles PUT /api/agents/:id/cron-jobs/:jid
func (h *CronJobHandler) UpdateCronJob(c *gin.Context) {
	agentID := c.Param("id")
	jobID := c.Param("jid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	job, err := h.mgr.GetStore().GetCronJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	if job.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	var req UpdateCronJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Apply partial updates
	if req.Type != nil {
		job.Type = *req.Type
	}
	if req.CronExpr != nil {
		if err := cronpkg.ValidateCronExpr(*req.CronExpr); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid cron expression: %v", err)})
			return
		}
		job.CronExpr = *req.CronExpr
	}
	if req.RunAt != nil {
		t, err := time.Parse(time.RFC3339, *req.RunAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid run_at: %v", err)})
			return
		}
		job.RunAt = &t
	}
	if req.Prompt != nil {
		job.Prompt = *req.Prompt
	}
	if req.SessionStrategy != nil {
		job.SessionStrategy = *req.SessionStrategy
	}
	if req.SessionID != nil {
		job.SessionID = *req.SessionID
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}

	job.UpdatedAt = time.Now()

	if err := h.mgr.GetStore().UpdateCronJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Re-register with scheduler
	if h.scheduler != nil {
		if job.Enabled {
			_ = h.scheduler.RegisterJob(job)
		} else {
			h.scheduler.UnregisterJob(job.ID)
		}
	}

	c.JSON(http.StatusOK, job)
}

// DeleteCronJob handles DELETE /api/agents/:id/cron-jobs/:jid
func (h *CronJobHandler) DeleteCronJob(c *gin.Context) {
	agentID := c.Param("id")
	jobID := c.Param("jid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	job, err := h.mgr.GetStore().GetCronJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	if job.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	// Unregister from scheduler
	if h.scheduler != nil {
		h.scheduler.UnregisterJob(jobID)
	}

	if err := h.mgr.GetStore().DeleteCronJob(jobID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// TriggerCronJob handles POST /api/agents/:id/cron-jobs/:jid/trigger
func (h *CronJobHandler) TriggerCronJob(c *gin.Context) {
	agentID := c.Param("id")
	jobID := c.Param("jid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	job, err := h.mgr.GetStore().GetCronJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	if job.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	// Execute asynchronously
	if h.scheduler != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			startTime := time.Now()
			execErr := h.scheduler.GetExecutor().ExecutePrompt(ctx, job.AgentID, job.SessionID, job.Prompt)
			duration := time.Since(startTime).Milliseconds()

			status := "success"
			errMsg := ""
			if execErr != nil {
				status = "failed"
				errMsg = execErr.Error()
			}

			now := time.Now()
			exec := &agent.CronJobExecution{
				ID:        fmt.Sprintf("exec_%d", now.UnixNano()),
				CronJobID: jobID,
				AgentID:   agentID,
				SessionID: job.SessionID,
				Status:    status,
				Error:     errMsg,
				Duration:  duration,
				StartedAt: startTime,
				CreatedAt: now,
			}
			_ = h.mgr.GetStore().CreateCronJobExecution(exec)
		}()
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "trigger accepted"})
}

// ListCronJobHistory handles GET /api/agents/:id/cron-jobs/:jid/history
func (h *CronJobHandler) ListCronJobHistory(c *gin.Context) {
	agentID := c.Param("id")
	jobID := c.Param("jid")

	if _, err := h.mgr.Get(agentID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	job, err := h.mgr.GetStore().GetCronJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	if job.AgentID != agentID {
		c.JSON(http.StatusNotFound, gin.H{"error": "cron job not found"})
		return
	}

	executions, err := h.mgr.GetStore().ListCronJobExecutions(jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if executions == nil {
		executions = []*agent.CronJobExecution{}
	}

	c.JSON(http.StatusOK, gin.H{"executions": executions})
}

// truncateCronTitle truncates a string for use as a session title.
func truncateCronTitle(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
