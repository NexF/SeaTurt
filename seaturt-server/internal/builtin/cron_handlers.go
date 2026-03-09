package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/seaturt/server/internal/agent"
	cronpkg "github.com/seaturt/server/internal/cron"
	"github.com/seaturt/server/internal/mcp"
)

// CronStore defines the persistence operations needed by cron handlers.
type CronStore interface {
	CreateCronJob(job *agent.CronJob) error
	GetCronJob(id string) (*agent.CronJob, error)
	ListCronJobs(agentID string) ([]*agent.CronJob, error)
	UpdateCronJob(job *agent.CronJob) error
	DeleteCronJob(id string) error
	CreateSession(s *agent.Session) error
}

// NewCronHandlers creates the handler map for all cron-related builtin tools.
func NewCronHandlers(store CronStore, scheduler *cronpkg.Scheduler) map[string]Handler {
	return map[string]Handler{
		"create_cron_job": &createCronJobHandler{store: store, scheduler: scheduler},
		"list_cron_jobs":  &listCronJobsHandler{store: store},
		"update_cron_job": &updateCronJobHandler{store: store, scheduler: scheduler},
		"delete_cron_job": &deleteCronJobHandler{store: store, scheduler: scheduler},
	}
}

// --- create_cron_job ---

type createCronJobHandler struct {
	store     CronStore
	scheduler *cronpkg.Scheduler
}

func (h *createCronJobHandler) Handle(_ context.Context, agentID string, args map[string]any) (*mcp.CallToolResult, error) {
	jobType := getString(args, "type")
	if jobType == "" {
		return errorResult("missing required field: type"), nil
	}
	prompt := getString(args, "prompt")
	if prompt == "" {
		return errorResult("missing required field: prompt"), nil
	}

	now := time.Now()
	job := &agent.CronJob{
		ID:              fmt.Sprintf("cron_%d", now.UnixNano()),
		AgentID:         agentID,
		Type:            jobType,
		Prompt:          prompt,
		SessionStrategy: getString(args, "session_strategy"),
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if job.SessionStrategy == "" {
		job.SessionStrategy = "fixed"
	}

	switch jobType {
	case "cron":
		cronExpr := getString(args, "cron_expr")
		if cronExpr == "" {
			return errorResult("cron_expr is required for type 'cron'"), nil
		}
		if err := cronpkg.ValidateCronExpr(cronExpr); err != nil {
			return errorResult(fmt.Sprintf("invalid cron expression: %v", err)), nil
		}
		job.CronExpr = cronExpr
	case "at":
		runAtStr := getString(args, "run_at")
		if runAtStr == "" {
			return errorResult("run_at is required for type 'at'"), nil
		}
		t, err := time.Parse(time.RFC3339, runAtStr)
		if err != nil {
			return errorResult(fmt.Sprintf("invalid run_at (must be RFC 3339): %v", err)), nil
		}
		if t.Before(time.Now()) {
			return errorResult("run_at must be in the future"), nil
		}
		job.RunAt = &t
	default:
		return errorResult(fmt.Sprintf("invalid type: %s (must be 'cron' or 'at')", jobType)), nil
	}

	// Auto-create session for fixed strategy
	if job.SessionStrategy == "fixed" {
		sess := &agent.Session{
			ID:        fmt.Sprintf("sess_%d", now.UnixNano()),
			AgentID:   agentID,
			Title:     fmt.Sprintf("定时任务: %s", truncateBuiltin(prompt, 20)),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := h.store.CreateSession(sess); err != nil {
			return errorResult(fmt.Sprintf("create session: %v", err)), nil
		}
		job.SessionID = sess.ID
	}

	if err := h.store.CreateCronJob(job); err != nil {
		return errorResult(fmt.Sprintf("create cron job: %v", err)), nil
	}

	// Register with scheduler
	if h.scheduler != nil {
		if err := h.scheduler.RegisterJob(job); err != nil {
			// Non-fatal
			return jsonResult(map[string]any{
				"cron_job": job,
				"warning":  fmt.Sprintf("scheduler registration failed: %v", err),
			}), nil
		}
	}

	return jsonResult(job), nil
}

// --- list_cron_jobs ---

type listCronJobsHandler struct {
	store CronStore
}

func (h *listCronJobsHandler) Handle(_ context.Context, agentID string, _ map[string]any) (*mcp.CallToolResult, error) {
	jobs, err := h.store.ListCronJobs(agentID)
	if err != nil {
		return errorResult(fmt.Sprintf("list cron jobs: %v", err)), nil
	}
	if jobs == nil {
		jobs = []*agent.CronJob{}
	}
	return jsonResult(map[string]any{"cron_jobs": jobs, "total": len(jobs)}), nil
}

// --- update_cron_job ---

type updateCronJobHandler struct {
	store     CronStore
	scheduler *cronpkg.Scheduler
}

func (h *updateCronJobHandler) Handle(_ context.Context, agentID string, args map[string]any) (*mcp.CallToolResult, error) {
	id := getString(args, "id")
	if id == "" {
		return errorResult("missing required field: id"), nil
	}

	job, err := h.store.GetCronJob(id)
	if err != nil {
		return errorResult(fmt.Sprintf("cron job not found: %s", id)), nil
	}
	if job.AgentID != agentID {
		return errorResult("cron job not found (wrong agent)"), nil
	}

	// Apply partial updates
	if v := getString(args, "cron_expr"); v != "" {
		if err := cronpkg.ValidateCronExpr(v); err != nil {
			return errorResult(fmt.Sprintf("invalid cron expression: %v", err)), nil
		}
		job.CronExpr = v
	}
	if v := getString(args, "run_at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return errorResult(fmt.Sprintf("invalid run_at: %v", err)), nil
		}
		job.RunAt = &t
	}
	if v := getString(args, "prompt"); v != "" {
		job.Prompt = v
	}
	if v, ok := getBool(args, "enabled"); ok {
		job.Enabled = v
	}

	job.UpdatedAt = time.Now()

	if err := h.store.UpdateCronJob(job); err != nil {
		return errorResult(fmt.Sprintf("update cron job: %v", err)), nil
	}

	// Re-register with scheduler
	if h.scheduler != nil {
		if job.Enabled {
			_ = h.scheduler.RegisterJob(job)
		} else {
			h.scheduler.UnregisterJob(job.ID)
		}
	}

	return jsonResult(job), nil
}

// --- delete_cron_job ---

type deleteCronJobHandler struct {
	store     CronStore
	scheduler *cronpkg.Scheduler
}

func (h *deleteCronJobHandler) Handle(_ context.Context, agentID string, args map[string]any) (*mcp.CallToolResult, error) {
	id := getString(args, "id")
	if id == "" {
		return errorResult("missing required field: id"), nil
	}

	job, err := h.store.GetCronJob(id)
	if err != nil {
		return errorResult(fmt.Sprintf("cron job not found: %s", id)), nil
	}
	if job.AgentID != agentID {
		return errorResult("cron job not found (wrong agent)"), nil
	}

	// Unregister from scheduler
	if h.scheduler != nil {
		h.scheduler.UnregisterJob(id)
	}

	if err := h.store.DeleteCronJob(id); err != nil {
		return errorResult(fmt.Sprintf("delete cron job: %v", err)), nil
	}

	return textResult(fmt.Sprintf("cron job %s deleted successfully", id)), nil
}

// truncateBuiltin truncates a string for display.
func truncateBuiltin(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
