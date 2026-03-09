package builtin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCronStore implements CronStore for testing.
type mockCronStore struct {
	jobs     map[string]*agent.CronJob
	sessions map[string]*agent.Session
}

func newMockCronStore() *mockCronStore {
	return &mockCronStore{
		jobs:     make(map[string]*agent.CronJob),
		sessions: make(map[string]*agent.Session),
	}
}

func (m *mockCronStore) CreateCronJob(job *agent.CronJob) error {
	m.jobs[job.ID] = job
	return nil
}
func (m *mockCronStore) GetCronJob(id string) (*agent.CronJob, error) {
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return j, nil
}
func (m *mockCronStore) ListCronJobs(agentID string) ([]*agent.CronJob, error) {
	var result []*agent.CronJob
	for _, j := range m.jobs {
		if j.AgentID == agentID {
			result = append(result, j)
		}
	}
	return result, nil
}
func (m *mockCronStore) UpdateCronJob(job *agent.CronJob) error {
	m.jobs[job.ID] = job
	return nil
}
func (m *mockCronStore) DeleteCronJob(id string) error {
	delete(m.jobs, id)
	return nil
}
func (m *mockCronStore) CreateSession(s *agent.Session) error {
	m.sessions[s.ID] = s
	return nil
}

// helper to create context with agentID
func ctxWithAgentID(agentID string) context.Context {
	return context.WithValue(context.Background(), agent.AgentIDContextKey, agentID)
}

func TestCreateCronJobHandler_Cron(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"type":      "cron",
		"cron_expr": "0 9 * * *",
		"prompt":    "daily news",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Len(t, store.jobs, 1)

	// Check session was auto-created
	assert.Len(t, store.sessions, 1)

	for _, job := range store.jobs {
		assert.Equal(t, "cron", job.Type)
		assert.Equal(t, "0 9 * * *", job.CronExpr)
		assert.Equal(t, "daily news", job.Prompt)
		assert.True(t, job.Enabled)
		assert.Equal(t, "fixed", job.SessionStrategy)
		assert.NotEmpty(t, job.SessionID)
	}
}

func TestCreateCronJobHandler_At(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	runAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"type":   "at",
		"run_at": runAt,
		"prompt": "reminder",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Len(t, store.jobs, 1)

	for _, job := range store.jobs {
		assert.Equal(t, "at", job.Type)
		assert.NotNil(t, job.RunAt)
		assert.Equal(t, "reminder", job.Prompt)
	}
}

func TestCreateCronJobHandler_MissingType(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"prompt": "test",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestCreateCronJobHandler_MissingPrompt(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"type":      "cron",
		"cron_expr": "0 9 * * *",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestCreateCronJobHandler_InvalidCronExpr(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"type":      "cron",
		"cron_expr": "bad expr",
		"prompt":    "test",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestCreateCronJobHandler_AtPastTime(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["create_cron_job"]

	pastTime := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"type":   "at",
		"run_at": pastTime,
		"prompt": "test",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestListCronJobsHandler(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_1", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "task 1", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	store.jobs["cron_2"] = &agent.CronJob{
		ID: "cron_2", AgentID: "agent_1", Type: "at",
		Prompt: "task 2", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	store.jobs["cron_3"] = &agent.CronJob{
		ID: "cron_3", AgentID: "agent_other", Type: "cron",
		Prompt: "other agent", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["list_cron_jobs"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", nil)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	// Should only contain 2 jobs for agent_1
	assert.Contains(t, result.Content[0].Text, "\"total\": 2")
}

func TestListCronJobsHandler_Empty(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["list_cron_jobs"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", nil)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "\"total\": 0")
}

func TestUpdateCronJobHandler(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_1", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "old prompt", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["update_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id":      "cron_1",
		"prompt":  "new prompt",
		"enabled": false,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	updated := store.jobs["cron_1"]
	assert.Equal(t, "new prompt", updated.Prompt)
	assert.False(t, updated.Enabled)
}

func TestUpdateCronJobHandler_WrongAgent(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_other", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["update_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id":     "cron_1",
		"prompt": "hacked",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError) // Security: wrong agent
}

func TestUpdateCronJobHandler_NotFound(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["update_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id": "nonexistent",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestUpdateCronJobHandler_InvalidCronExpr(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_1", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["update_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id":        "cron_1",
		"cron_expr": "bad expr",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestDeleteCronJobHandler(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_1", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "to delete", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["delete_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id": "cron_1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Empty(t, store.jobs)
}

func TestDeleteCronJobHandler_WrongAgent(t *testing.T) {
	store := newMockCronStore()
	now := time.Now()
	store.jobs["cron_1"] = &agent.CronJob{
		ID: "cron_1", AgentID: "agent_other", Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}

	handlers := NewCronHandlers(store, nil)
	h := handlers["delete_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{
		"id": "cron_1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError) // Security: wrong agent
	assert.Len(t, store.jobs, 1)  // Not deleted
}

func TestDeleteCronJobHandler_MissingID(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	h := handlers["delete_cron_job"]

	result, err := h.Handle(ctxWithAgentID("agent_1"), "agent_1", map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestBuiltinRouter_AllToolsReturnsQualifiedNames verifies tool name prefixing.
func TestBuiltinRouter_AllToolsReturnsQualifiedNames(t *testing.T) {
	handlers := NewCronHandlers(newMockCronStore(), nil)
	router := NewRouter(handlers)

	tools := router.AllTools()
	assert.Len(t, tools, 4)

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	assert.True(t, names["builtin-create_cron_job"])
	assert.True(t, names["builtin-list_cron_jobs"])
	assert.True(t, names["builtin-update_cron_job"])
	assert.True(t, names["builtin-delete_cron_job"])
}

// TestBuiltinRouter_Route verifies routing dispatches correctly.
func TestBuiltinRouter_Route(t *testing.T) {
	store := newMockCronStore()
	handlers := NewCronHandlers(store, nil)
	router := NewRouter(handlers)

	ctx := context.WithValue(context.Background(), agent.AgentIDContextKey, "agent_1")

	// Route a list call
	result, err := router.Route(ctx, "builtin-list_cron_jobs", nil)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Route unknown tool
	_, err = router.Route(ctx, "builtin-unknown_tool", nil)
	assert.Error(t, err)

	// Route without agentID
	_, err = router.Route(context.Background(), "builtin-list_cron_jobs", nil)
	assert.Error(t, err)
}
