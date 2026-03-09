package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CronJob API integration tests ---

func TestCronJobAPI_ListCronJobs_Empty(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		CronJobs []agent.CronJob `json:"cron_jobs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.CronJobs)
}

func TestCronJobAPI_CreateCronJob_Cron(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"0 9 * * *","prompt":"每日新闻摘要"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var job agent.CronJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &job))
	assert.Equal(t, "cron", job.Type)
	assert.Equal(t, "0 9 * * *", job.CronExpr)
	assert.Equal(t, "每日新闻摘要", job.Prompt)
	assert.True(t, job.Enabled)
	assert.Equal(t, "fixed", job.SessionStrategy)
	assert.NotEmpty(t, job.SessionID) // auto-created session
}

func TestCronJobAPI_CreateCronJob_At(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	runAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"at","run_at":"`+runAt+`","prompt":"提醒我开会"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var job agent.CronJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &job))
	assert.Equal(t, "at", job.Type)
	assert.NotNil(t, job.RunAt)
	assert.Equal(t, "提醒我开会", job.Prompt)
}

func TestCronJobAPI_CreateCronJob_InvalidCronExpr(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"bad expr","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_CronMissingExpr(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_AtMissingRunAt(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"at","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_AtPastTime(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	pastTime := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"at","run_at":"`+pastTime+`","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_MissingPrompt(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"0 9 * * *"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_InvalidType(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"invalid","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCronJobAPI_CreateCronJob_AgentNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/nonexistent/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"0 9 * * *","prompt":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCronJobAPI_GetCronJob(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	// Seed cron job directly
	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_get_api", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", SessionStrategy: "fixed", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs/"+job.ID, nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var got agent.CronJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "cron_get_api", got.ID)
}

func TestCronJobAPI_GetCronJob_NotFound(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs/nonexistent", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCronJobAPI_GetCronJob_WrongAgent(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	ag2 := &agent.Agent{
		ID: "agent_cron_test_2", Name: "other", Status: agent.StatusRunning,
		Config: agent.AgentConfig{Model: "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAgent(ag2))

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_wrong_agent", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag2.ID+"/cron-jobs/"+job.ID, nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCronJobAPI_UpdateCronJob(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_upd_api", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "旧任务", SessionStrategy: "fixed", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/agents/"+ag.ID+"/cron-jobs/"+job.ID,
		strings.NewReader(`{"prompt":"新任务","enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var updated agent.CronJob
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, "新任务", updated.Prompt)
	assert.False(t, updated.Enabled)
}

func TestCronJobAPI_DeleteCronJob(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_del_api", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "即将删除", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/agents/"+ag.ID+"/cron-jobs/"+job.ID, nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	_, err := s.GetCronJob(job.ID)
	assert.Error(t, err)
}

func TestCronJobAPI_ListCronJobs_AfterCreate(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	// Create via API
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"0 9 * * *","prompt":"task 1"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/cron-jobs",
		strings.NewReader(`{"type":"cron","cron_expr":"0 18 * * *","prompt":"task 2"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// List
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		CronJobs []agent.CronJob `json:"cron_jobs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.CronJobs, 2)
}

func TestCronJobAPI_ListHistory_Empty(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_hist", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs/"+job.ID+"/history", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Executions []agent.CronJobExecution `json:"executions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Executions)
}

func TestCronJobAPI_ListHistory_WithExecutions(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	job := &agent.CronJob{
		ID: "cron_hist2", AgentID: ag.ID, Type: "cron", CronExpr: "0 9 * * *",
		Prompt: "test", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateCronJob(job))

	exec := &agent.CronJobExecution{
		ID: "exec_hist", CronJobID: job.ID, AgentID: ag.ID,
		Status: "success", Duration: 500, StartedAt: now, CreatedAt: now,
	}
	require.NoError(t, s.CreateCronJobExecution(exec))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/cron-jobs/"+job.ID+"/history", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Executions []agent.CronJobExecution `json:"executions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Executions, 1)
	assert.Equal(t, "success", body.Executions[0].Status)
}

func TestCronJobAPI_ListCronJobs_AgentNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/nonexistent/cron-jobs", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
