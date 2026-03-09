package cron

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/seaturt/server/internal/agent"
)

// Store defines the persistence operations needed by Scheduler.
type Store interface {
	GetCronJob(id string) (*agent.CronJob, error)
	ListAllEnabledCronJobs() ([]*agent.CronJob, error)
	UpdateCronJob(job *agent.CronJob) error
	CreateCronJobExecution(exec *agent.CronJobExecution) error
}

// AgentExecutor defines how the scheduler triggers an agent to run a prompt.
type AgentExecutor interface {
	// ExecutePrompt sends a prompt to an agent session and returns error.
	// The executor is responsible for auto-starting the agent if needed.
	ExecutePrompt(ctx context.Context, agentID, sessionID, prompt string) error
}

// Scheduler manages cron and at-type scheduled tasks.
type Scheduler struct {
	mu       sync.Mutex
	cron     *cron.Cron
	store    Store
	executor AgentExecutor

	// entryIDs maps CronJob.ID -> cron.EntryID for cron-type jobs
	entryIDs map[string]cron.EntryID

	// atTimers maps CronJob.ID -> *time.Timer for at-type jobs
	atTimers map[string]*time.Timer

	// running tracks which jobs are currently executing (for skip/overlap protection)
	running map[string]bool
}

// NewScheduler creates a new Scheduler.
func NewScheduler(store Store, executor AgentExecutor) *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithParser(cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		))),
		store:    store,
		executor: executor,
		entryIDs: make(map[string]cron.EntryID),
		atTimers: make(map[string]*time.Timer),
		running:  make(map[string]bool),
	}
}

// Start starts the cron scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
	slog.Info("cron scheduler started")
}

// Stop stops the cron scheduler and cancels all at-timers.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()

	s.mu.Lock()
	for id, timer := range s.atTimers {
		timer.Stop()
		delete(s.atTimers, id)
	}
	s.mu.Unlock()

	slog.Info("cron scheduler stopped")
}

// GetExecutor returns the AgentExecutor for manual trigger use.
func (s *Scheduler) GetExecutor() AgentExecutor {
	return s.executor
}

// LoadAll loads all enabled CronJobs from DB and registers them.
func (s *Scheduler) LoadAll() error {
	jobs, err := s.store.ListAllEnabledCronJobs()
	if err != nil {
		return fmt.Errorf("list enabled cron jobs: %w", err)
	}

	loaded := 0
	for _, job := range jobs {
		if err := s.RegisterJob(job); err != nil {
			slog.Warn("failed to register cron job on load",
				"id", job.ID, "type", job.Type, "err", err)
			continue
		}
		loaded++
	}

	slog.Info("cron jobs loaded", "total", loaded, "enabled", len(jobs))
	return nil
}

// RegisterJob registers a CronJob with the scheduler.
func (s *Scheduler) RegisterJob(job *agent.CronJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing registration if any
	s.unregisterLocked(job.ID)

	if !job.Enabled {
		return nil
	}

	switch job.Type {
	case "cron":
		return s.registerCronJob(job)
	case "at":
		return s.registerAtJob(job)
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}

// UnregisterJob removes a CronJob from the scheduler.
func (s *Scheduler) UnregisterJob(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unregisterLocked(jobID)
}

func (s *Scheduler) unregisterLocked(jobID string) {
	if entryID, ok := s.entryIDs[jobID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, jobID)
	}
	if timer, ok := s.atTimers[jobID]; ok {
		timer.Stop()
		delete(s.atTimers, jobID)
	}
}

func (s *Scheduler) registerCronJob(job *agent.CronJob) error {
	entryID, err := s.cron.AddFunc(job.CronExpr, func() {
		s.executeJob(job.ID)
	})
	if err != nil {
		return fmt.Errorf("add cron entry: %w", err)
	}
	s.entryIDs[job.ID] = entryID

	// Update next_run_at
	entry := s.cron.Entry(entryID)
	nextRun := entry.Next
	job.NextRunAt = &nextRun
	_ = s.store.UpdateCronJob(job)

	slog.Info("registered cron job", "id", job.ID, "expr", job.CronExpr, "next", nextRun)
	return nil
}

func (s *Scheduler) registerAtJob(job *agent.CronJob) error {
	if job.RunAt == nil {
		return fmt.Errorf("at job has no run_at time")
	}

	delay := time.Until(*job.RunAt)
	if delay <= 0 {
		// Already past — execute immediately
		go s.executeJob(job.ID)
		return nil
	}

	timer := time.AfterFunc(delay, func() {
		s.executeJob(job.ID)
	})
	s.atTimers[job.ID] = timer

	job.NextRunAt = job.RunAt
	_ = s.store.UpdateCronJob(job)

	slog.Info("registered at job", "id", job.ID, "run_at", job.RunAt, "delay", delay)
	return nil
}

// executeJob is the core execution logic, called when a job triggers.
func (s *Scheduler) executeJob(jobID string) {
	// Skip protection: check if already running
	s.mu.Lock()
	if s.running[jobID] {
		s.mu.Unlock()
		slog.Info("skipping overlapping execution", "job_id", jobID)
		s.recordExecution(jobID, "skipped", "", 0)
		return
	}
	s.running[jobID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, jobID)
		s.mu.Unlock()
	}()

	// Reload job from DB to get latest state
	job, err := s.store.GetCronJob(jobID)
	if err != nil {
		slog.Error("failed to get cron job for execution", "id", jobID, "err", err)
		return
	}

	if !job.Enabled {
		slog.Info("job disabled, skipping", "id", jobID)
		return
	}

	startTime := time.Now()

	// Execute the prompt
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	execErr := s.executor.ExecutePrompt(ctx, job.AgentID, job.SessionID, job.Prompt)

	duration := time.Since(startTime).Milliseconds()

	// Update last_run_at
	now := time.Now()
	job.LastRunAt = &now

	// For at-type jobs, disable after execution
	if job.Type == "at" {
		job.Enabled = false
		job.NextRunAt = nil
		s.mu.Lock()
		delete(s.atTimers, jobID)
		s.mu.Unlock()
	} else {
		// Update next_run_at for cron jobs
		s.mu.Lock()
		if entryID, ok := s.entryIDs[jobID]; ok {
			entry := s.cron.Entry(entryID)
			nextRun := entry.Next
			job.NextRunAt = &nextRun
		}
		s.mu.Unlock()
	}

	job.UpdatedAt = now
	_ = s.store.UpdateCronJob(job)

	// Record execution
	status := "success"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
		slog.Error("cron job execution failed", "id", jobID, "err", execErr)
	} else {
		slog.Info("cron job executed", "id", jobID, "duration_ms", duration)
	}

	s.recordExecution(jobID, status, errMsg, duration)
}

func (s *Scheduler) recordExecution(jobID, status, errMsg string, durationMs int64) {
	job, err := s.store.GetCronJob(jobID)
	if err != nil {
		slog.Error("failed to get job for execution record", "id", jobID, "err", err)
		return
	}

	now := time.Now()
	exec := &agent.CronJobExecution{
		ID:        fmt.Sprintf("exec_%d", now.UnixNano()),
		CronJobID: jobID,
		AgentID:   job.AgentID,
		SessionID: job.SessionID,
		Status:    status,
		Error:     errMsg,
		Duration:  durationMs,
		StartedAt: now.Add(-time.Duration(durationMs) * time.Millisecond),
		CreatedAt: now,
	}

	if err := s.store.CreateCronJobExecution(exec); err != nil {
		slog.Error("failed to record execution", "id", jobID, "err", err)
	}
}

// ValidateCronExpr validates a cron expression without registering it.
func ValidateCronExpr(expr string) error {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	return err
}

// NextRunTimes returns the next n run times for a cron expression.
func NextRunTimes(expr string, n int) ([]time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, err
	}

	var times []time.Time
	t := time.Now()
	for i := 0; i < n; i++ {
		t = schedule.Next(t)
		times = append(times, t)
	}
	return times, nil
}
