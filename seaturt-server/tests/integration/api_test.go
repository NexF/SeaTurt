//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/api"
	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer creates a complete API server stack with a mock LLM for testing.
func newTestServer(t *testing.T, mockResponses []MockLLMResponse) (*httptest.Server, *MockLLMServer) {
	t.Helper()

	// Create temp DB and workspace for this test
	tmpDB, err := os.CreateTemp("", "api-test-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	tmpDB.Close()

	wsRoot, err := os.MkdirTemp(testWorkspace, "api-ws-*")
	require.NoError(t, err)

	testStore, err := store.New(dbPath)
	require.NoError(t, err)

	mockLLM := NewMockLLMServer(mockResponses)
	llmClient := mockLLM.NewClient()

	cfg := &config.Config{
		ServerPort:   0,
		SandboxImage: testImage,
		WorkspaceRoot: wsRoot,
		DefaultModel: "test-model",
		DefaultMCPServers: []config.MCPServerConfig{
			{Name: "core", Command: "mcp-server-core"},
		},
	}

	agentMgr := agentpkg.NewManager(cfg, testStore, dockerMgr, llmClient)
	server := api.NewServer(0, agentMgr, 20*1024*1024)

	// Use httptest to wrap the gin engine
	_ = server // We need the engine, let's access it through the Server
	ts := httptest.NewServer(getEngineFromServer(server))

	t.Cleanup(func() {
		ts.Close()
		mockLLM.Close()
		testStore.Close()
		os.Remove(dbPath)
		os.RemoveAll(wsRoot)
		// Cleanup containers
		agents, _ := agentMgr.List()
		for _, ag := range agents {
			_ = agentMgr.Delete(context.Background(), ag.ID)
		}
	})

	return ts, mockLLM
}

// IT-10: Agent CRUD API — create, get, list, stop, start, delete
func TestAgentCRUDAPI(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// 1. List agents — should be empty
	resp := doRequest(t, ts, "GET", "/api/agents", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var agents []agentpkg.Agent
	decodeJSON(t, resp, &agents)
	assert.Empty(t, agents)

	// 2. Create agent
	createBody := map[string]any{
		"name": "test-agent",
	}
	resp = doRequest(t, ts, "POST", "/api/agents", createBody)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created agentpkg.Agent
	decodeJSON(t, resp, &created)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "test-agent", created.Name)
	assert.Equal(t, agentpkg.StatusRunning, created.Status)
	assert.NotEmpty(t, created.ContainerID)

	agentID := created.ID

	// 3. Get agent by ID
	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var fetched agentpkg.Agent
	decodeJSON(t, resp, &fetched)
	assert.Equal(t, agentID, fetched.ID)
	assert.Equal(t, agentpkg.StatusRunning, fetched.Status)

	// 4. List agents — should have 1
	resp = doRequest(t, ts, "GET", "/api/agents", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &agents)
	assert.Len(t, agents, 1)

	// 5. Stop agent
	resp = doRequest(t, ts, "POST", "/api/agents/"+agentID+"/stop", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID, nil)
	decodeJSON(t, resp, &fetched)
	assert.Equal(t, agentpkg.StatusStopped, fetched.Status)

	// 6. Start agent (resume)
	resp = doRequest(t, ts, "POST", "/api/agents/"+agentID+"/start", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID, nil)
	decodeJSON(t, resp, &fetched)
	assert.Equal(t, agentpkg.StatusRunning, fetched.Status)

	// 7. Delete agent
	resp = doRequest(t, ts, "DELETE", "/api/agents/"+agentID, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 8. Get deleted agent — should 404
	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// IT-10b: Agent Create — validation
func TestAgentCreateValidation(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Missing name
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// IT-11: Chat API — SSE streaming response
func TestChatAPIStreaming(t *testing.T) {
	t.Parallel()
	// Setup mock LLM that will be used during chat
	mockResponses := []MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "shell_exec",
						Arguments: `{"command":"echo hello_from_chat_test"}`,
					},
				},
			},
		},
		{
			Content: "Command output: hello_from_chat_test",
		},
	}

	ts, mockLLM := newTestServer(t, mockResponses)
	_ = mockLLM

	// Create agent first
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "chat-test-agent",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created agentpkg.Agent
	decodeJSON(t, resp, &created)
	agentID := created.ID

	// Send chat message
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "say hello"}},
	})
	chatReq, err := http.NewRequest("POST", ts.URL+"/api/agents/"+agentID+"/chat", bytes.NewReader(chatBody))
	require.NoError(t, err)
	chatReq.Header.Set("Content-Type", "application/json")

	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)
	assert.Contains(t, chatResp.Header.Get("Content-Type"), "text/event-stream")

	// Parse SSE events (support both "data: " and "data:" formats)
	scanner := bufio.NewScanner(chatResp.Body)
	var events []agentpkg.StreamEvent

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")

		var event agentpkg.StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		events = append(events, event)
	}

	// Verify we received expected event types
	eventTypes := make(map[string]int)
	for _, e := range events {
		eventTypes[e.Type]++
	}

	assert.Greater(t, eventTypes["tool_call"], 0, "should have tool_call events")
	assert.Greater(t, eventTypes["tool_result"], 0, "should have tool_result events")

	// Verify history was saved
	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var messages []agentpkg.Message
	decodeJSON(t, resp, &messages)
	assert.GreaterOrEqual(t, len(messages), 2, "should have at least user + assistant messages")

	// Clear history
	resp = doRequest(t, ts, "DELETE", "/api/agents/"+agentID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doRequest(t, ts, "GET", "/api/agents/"+agentID+"/history", nil)
	decodeJSON(t, resp, &messages)
	assert.Empty(t, messages)

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+agentID, nil)
}

// IT-11b: Chat API — agent not running
func TestChatAPIAgentNotRunning(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Create and stop agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "stopped-chat"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	doRequest(t, ts, "POST", "/api/agents/"+ag.ID+"/stop", nil)

	// Try to chat
	resp = doRequest(t, ts, "POST", "/api/agents/"+ag.ID+"/chat", map[string]any{
		"content": []map[string]string{{"type": "text", "text": "hello"}},
	})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// --- Helpers ---

func doRequest(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()

	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, ts.URL+path, bodyReader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	err := json.NewDecoder(resp.Body).Decode(target)
	require.NoError(t, err)
}

// getEngineFromServer extracts the underlying http.Handler from api.Server.
func getEngineFromServer(s *api.Server) http.Handler {
	return s
}
