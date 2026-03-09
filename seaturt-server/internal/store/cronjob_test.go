package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CronJob CRUD tests ---

func TestCronJobCRUD_CreateAndList(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job1 := &agent.CronJob{
		ID: "cron_1", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "每日新闻", SessionStrategy: "fixed", SessionID: "sess_1",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	job2 := &agent.CronJob{
		ID: "cron_2", AgentID: ag.ID, Type: "at",
		Prompt: "提醒开会", SessionStrategy: "fixed", SessionID: "sess_2",
		Enabled: true, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}
	runAt := now.Add(24 * time.Hour)
	job2.RunAt = &runAt

	require.NoError(t, s.CreateCronJob(job1))
	require.NoError(t, s.CreateCronJob(job2))

	jobs, err := s.ListCronJobs(ag.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	// Ordered by created_at DESC
	assert.Equal(t, "cron_2", jobs[0].ID)
	assert.Equal(t, "cron_1", jobs[1].ID)
}

func TestCronJobCRUD_GetCronJob(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_get", AgentID: ag.ID, Type: "cron", CronExpr: "*/5 * * * *",
		Prompt: "检查状态", SessionStrategy: "new", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	got, err := s.GetCronJob("cron_get")
	require.NoError(t, err)
	assert.Equal(t, "cron", got.Type)
	assert.Equal(t, "*/5 * * * *", got.CronExpr)
	assert.Equal(t, "检查状态", got.Prompt)
	assert.True(t, got.Enabled)
}

func TestCronJobCRUD_GetCronJob_NotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	_, err := s.GetCronJob("nonexistent")
	assert.Error(t, err)
}

func TestCronJobCRUD_UpdateCronJob(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_upd", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "旧任务", SessionStrategy: "fixed", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	job.Prompt = "新任务"
	job.Enabled = false
	lastRun := now.Add(-time.Hour)
	job.LastRunAt = &lastRun
	job.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, s.UpdateCronJob(job))

	got, err := s.GetCronJob("cron_upd")
	require.NoError(t, err)
	assert.Equal(t, "新任务", got.Prompt)
	assert.False(t, got.Enabled)
	require.NotNil(t, got.LastRunAt)
}

func TestCronJobCRUD_DeleteCronJob(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_del", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "即将删除", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	// Add an execution
	exec := &agent.CronJobExecution{
		ID: "exec_1", CronJobID: "cron_del", AgentID: ag.ID,
		Status: "success", Duration: 100, StartedAt: now, CreatedAt: now,
	}
	require.NoError(t, s.CreateCronJobExecution(exec))

	// Delete job (should cascade delete executions)
	require.NoError(t, s.DeleteCronJob("cron_del"))

	_, err := s.GetCronJob("cron_del")
	assert.Error(t, err)

	execs, err := s.ListCronJobExecutions("cron_del")
	require.NoError(t, err)
	assert.Empty(t, execs)
}

func TestCronJobCRUD_ListAllEnabled(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	for i := 0; i < 3; i++ {
		enabled := i < 2 // 2 enabled, 1 disabled
		job := &agent.CronJob{
			ID: fmt.Sprintf("cron_en_%d", i), AgentID: ag.ID, Type: "cron",
			CronExpr: "0 9 * * *", Prompt: fmt.Sprintf("task %d", i),
			Enabled: enabled, CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, s.CreateCronJob(job))
	}

	jobs, err := s.ListAllEnabledCronJobs()
	require.NoError(t, err)
	assert.Len(t, jobs, 2)
}

func TestCronJobCRUD_AtTypeWithRunAt(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	runAt := now.Add(24 * time.Hour)
	job := &agent.CronJob{
		ID: "cron_at", AgentID: ag.ID, Type: "at", RunAt: &runAt,
		Prompt: "提醒我开会", SessionStrategy: "fixed", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	got, err := s.GetCronJob("cron_at")
	require.NoError(t, err)
	assert.Equal(t, "at", got.Type)
	require.NotNil(t, got.RunAt)
	// Compare rounded to seconds (SQLite datetime precision)
	assert.WithinDuration(t, runAt, *got.RunAt, time.Second)
}

func TestCronJobCRUD_DeleteByAgent(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	for i := 0; i < 3; i++ {
		job := &agent.CronJob{
			ID: fmt.Sprintf("cron_cascade_%d", i), AgentID: ag.ID, Type: "cron",
			CronExpr: "0 9 * * *", Prompt: "task", Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, s.CreateCronJob(job))

		exec := &agent.CronJobExecution{
			ID: fmt.Sprintf("exec_cascade_%d", i), CronJobID: job.ID, AgentID: ag.ID,
			Status: "success", Duration: 100, StartedAt: now, CreatedAt: now,
		}
		require.NoError(t, s.CreateCronJobExecution(exec))
	}

	require.NoError(t, s.DeleteCronJobsByAgent(ag.ID))

	jobs, err := s.ListCronJobs(ag.ID)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// --- CronJobExecution tests ---

func TestCronJobExecution_CreateAndList(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_exec_test", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	for i := 0; i < 5; i++ {
		exec := &agent.CronJobExecution{
			ID: fmt.Sprintf("exec_%d", i), CronJobID: job.ID, AgentID: ag.ID,
			Status: "success", Duration: int64(100 + i*10),
			StartedAt: now.Add(time.Duration(i) * time.Minute), CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		require.NoError(t, s.CreateCronJobExecution(exec))
	}

	execs, err := s.ListCronJobExecutions(job.ID)
	require.NoError(t, err)
	require.Len(t, execs, 5)
	// Ordered by created_at DESC
	assert.Equal(t, "exec_4", execs[0].ID)
	assert.Equal(t, "exec_0", execs[4].ID)
}

func TestCronJobExecution_StatusVariety(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ag := newTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_status", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	statuses := []string{"success", "failed", "skipped"}
	for i, status := range statuses {
		exec := &agent.CronJobExecution{
			ID: fmt.Sprintf("exec_st_%d", i), CronJobID: job.ID, AgentID: ag.ID,
			Status: status, Error: "", Duration: 100,
			StartedAt: now, CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		if status == "failed" {
			exec.Error = "timeout"
		}
		require.NoError(t, s.CreateCronJobExecution(exec))
	}

	execs, err := s.ListCronJobExecutions(job.ID)
	require.NoError(t, err)
	require.Len(t, execs, 3)

	// Find the failed one
	var failedExec *agent.CronJobExecution
	for _, e := range execs {
		if e.Status == "failed" {
			failedExec = e
			break
		}
	}
	require.NotNil(t, failedExec)
	assert.Equal(t, "timeout", failedExec.Error)
}
