//go:build integration

package integration

import (
	"net/http"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-65: Chat history — send message, load history, clear history.
func TestHistory_LoadAndClear(t *testing.T) {
	t.Parallel()
	mockResponses := []MockLLMResponse{
		{Content: "First response"},
		{Content: "Second response"},
	}
	ts, _ := newTestServer(t, mockResponses)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "history-crud"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// History should be empty initially
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var messages []agentpkg.Message
	decodeJSON(t, resp, &messages)
	assert.Empty(t, messages, "history should be empty initially")

	// Send first chat message
	sendChat(t, ts, ag.ID, "hello")

	// History should have user + assistant messages
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &messages)
	assert.GreaterOrEqual(t, len(messages), 2, "should have user + assistant messages")

	// Verify message roles
	hasUser := false
	hasAssistant := false
	for _, m := range messages {
		switch m.Role {
		case "user":
			hasUser = true
		case "assistant":
			hasAssistant = true
		}
	}
	assert.True(t, hasUser, "should have user message")
	assert.True(t, hasAssistant, "should have assistant message")

	// Send second message to accumulate history
	sendChat(t, ts, ag.ID, "follow up")

	// History should have more messages
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &messages)
	assert.GreaterOrEqual(t, len(messages), 4, "should have messages from two rounds")

	// Clear history
	resp = doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// History should be empty after clearing
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	decodeJSON(t, resp, &messages)
	assert.Empty(t, messages, "history should be empty after clearing")
}

// IT-66: History for non-existent agent returns 404.
func TestHistory_AgentNotFound(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "GET", "/api/agents/nonexistent/history", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp = doRequest(t, ts, "DELETE", "/api/agents/nonexistent/history", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// IT-67: History messages have correct structure (id, agent_id, role, content, created_at).
func TestHistory_MessageStructure(t *testing.T) {
	t.Parallel()
	mockResponses := []MockLLMResponse{
		{Content: "Structured response"},
	}
	ts, _ := newTestServer(t, mockResponses)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "history-struct"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	sendChat(t, ts, ag.ID, "test message")

	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var messages []agentpkg.Message
	decodeJSON(t, resp, &messages)
	require.GreaterOrEqual(t, len(messages), 2)

	for _, m := range messages {
		assert.NotEmpty(t, m.ID, "message should have ID")
		assert.Equal(t, ag.ID, m.AgentID, "message should reference correct agent")
		assert.NotEmpty(t, m.Role, "message should have role")
		assert.NotNil(t, m.Content, "message should have content")
		assert.False(t, m.CreatedAt.IsZero(), "message should have created_at")
	}
}
