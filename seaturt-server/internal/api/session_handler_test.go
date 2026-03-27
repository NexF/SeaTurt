package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestServer creates a minimal Server with a real store for session API testing.
func setupTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "session-api-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	tmpDB.Close()

	s, err := store.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		s.Close()
		os.Remove(dbPath)
	})

	cfg := &config.Config{}
	mgr := agent.NewManager(cfg, s, nil, nil)
	srv := NewServer(0, mgr, 0, nil)

	return srv, s
}

// seedTestAgent inserts a test agent directly into the store.
func seedTestAgent(t *testing.T, s *store.Store) *agent.Agent {
	t.Helper()
	ag := &agent.Agent{
		ID:        "agent_api_test",
		Name:      "test-agent",
		Status:    agent.StatusRunning,
		Config:    agent.AgentConfig{Model: "test-model"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAgent(ag))
	return ag
}

// --- Session CRUD integration tests ---

func TestSessionAPI_ListSessions_Empty(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/sessions", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Sessions []agent.Session `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Sessions)
}

func TestSessionAPI_CreateSession_Default(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/sessions",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var sess agent.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sess))
	assert.Equal(t, "新对话", sess.Title)
	assert.Equal(t, ag.ID, sess.AgentID)
	assert.NotEmpty(t, sess.ID)
}

func TestSessionAPI_CreateSession_CustomTitle(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/sessions",
		strings.NewReader(`{"title":"代码审查"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var sess agent.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sess))
	assert.Equal(t, "代码审查", sess.Title)
}

func TestSessionAPI_CreateSession_AgentNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agents/nonexistent/sessions",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSessionAPI_UpdateSession(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	// Create session first
	now := time.Now()
	sess := &agent.Session{
		ID: "sess_upd_api", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/agents/"+ag.ID+"/sessions/"+sess.ID,
		strings.NewReader(`{"title":"帮我写代码"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var updated agent.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, "帮我写代码", updated.Title)
}

func TestSessionAPI_UpdateSession_MissingTitle(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_upd_bad", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/agents/"+ag.ID+"/sessions/"+sess.ID,
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSessionAPI_UpdateSession_NotFound(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/agents/"+ag.ID+"/sessions/nonexistent",
		strings.NewReader(`{"title":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSessionAPI_UpdateSession_WrongAgent(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	// Create a second agent
	ag2 := &agent.Agent{
		ID: "agent_api_test_2", Name: "other", Status: agent.StatusRunning,
		Config: agent.AgentConfig{Model: "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAgent(ag2))

	// Session belongs to ag, not ag2
	now := time.Now()
	sess := &agent.Session{
		ID: "sess_wrong_agent", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/agents/"+ag2.ID+"/sessions/"+sess.ID,
		strings.NewReader(`{"title":"hacked"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSessionAPI_DeleteSession(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_del_api", AgentID: ag.ID, Title: "即将删除",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/agents/"+ag.ID+"/sessions/"+sess.ID, nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify it's gone
	_, err := s.GetSession(sess.ID)
	assert.Error(t, err)
}

func TestSessionAPI_DeleteSession_NotFound(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/agents/"+ag.ID+"/sessions/nonexistent", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSessionAPI_DeleteSession_WrongAgent(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	ag2 := &agent.Agent{
		ID: "agent_api_test_3", Name: "other2", Status: agent.StatusRunning,
		Config: agent.AgentConfig{Model: "test"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, s.CreateAgent(ag2))

	now := time.Now()
	sess := &agent.Session{
		ID: "sess_del_wrong", AgentID: ag.ID, Title: "新对话",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, s.CreateSession(sess))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/agents/"+ag2.ID+"/sessions/"+sess.ID, nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// Session should still exist
	got, err := s.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "新对话", got.Title)
}

func TestSessionAPI_ListSessions_AfterCreate(t *testing.T) {
	srv, s := setupTestServer(t)
	ag := seedTestAgent(t, s)

	// Create two sessions via API
	for _, title := range []string{"会话A", "会话B"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/agents/"+ag.ID+"/sessions",
			strings.NewReader(`{"title":"`+title+`"}`))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
	}

	// List
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/"+ag.ID+"/sessions", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Sessions []agent.Session `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Sessions, 2)
}

func TestSessionAPI_ListSessions_AgentNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/agents/nonexistent/sessions", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// truncateTitle 单元测试
func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		input    string
		maxRunes int
		expected string
	}{
		{"hello", 20, "hello"},
		{"这是一段超过二十个字符的中文标题，需要被截断处理", 20, "这是一段超过二十个字符的中文标题，需要被"},
		{"short", 3, "sho"},
		{"", 20, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, truncateTitle(tt.input, tt.maxRunes))
	}
}
