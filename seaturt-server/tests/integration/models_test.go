//go:build integration

package integration

import (
	"net/http"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-49: GET /api/models returns available models with id, name, provider.
func TestModelsAPI(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "GET", "/api/models", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Models []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
		} `json:"models"`
		DefaultModel string `json:"default_model"`
	}
	decodeJSON(t, resp, &result)

	// Default model should be "test-model" from newTestServer config
	assert.Equal(t, "test-model", result.DefaultModel)

	// Models list may be empty if no providers configured in test config,
	// but the response structure should be valid
	assert.NotNil(t, result.Models)
}

// IT-50: GET /api/models with providers configured returns correct model entries.
func TestModelsAPI_WithProviders(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServerWithProviders(t, nil)

	resp := doRequest(t, ts, "GET", "/api/models", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Models []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
		} `json:"models"`
		DefaultModel string `json:"default_model"`
	}
	decodeJSON(t, resp, &result)

	assert.Equal(t, "test-model", result.DefaultModel)
	require.GreaterOrEqual(t, len(result.Models), 2, "should have at least 2 models")

	// Verify each model has required fields
	for _, m := range result.Models {
		assert.NotEmpty(t, m.ID, "model id should not be empty")
		assert.NotEmpty(t, m.Name, "model name should not be empty")
		assert.NotEmpty(t, m.Provider, "model provider should not be empty")
	}

	// Verify model with name set correctly
	found := false
	for _, m := range result.Models {
		if m.ID == "gpt-4o" {
			assert.Equal(t, "GPT-4o", m.Name)
			assert.Equal(t, "test-provider", m.Provider)
			found = true
			break
		}
	}
	assert.True(t, found, "should find gpt-4o model")
}

// IT-51: Agent name allows arbitrary characters (Chinese, spaces, symbols).
func TestAgentCreate_ArbitraryName(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	testCases := []struct {
		name     string
		agentName string
	}{
		{"Chinese", "我的编程助手"},
		{"Spaces", "My Coding Assistant"},
		{"Symbols", "agent-v2.0 (test)"},
		{"Mixed", "全栈助手 v2.0 🚀"},
		{"Emoji", "🐢 SeaTurt Agent"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
				"name": tc.agentName,
			})
			require.Equal(t, http.StatusCreated, resp.StatusCode)

			var ag agentpkg.Agent
			decodeJSON(t, resp, &ag)
			defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

			assert.Equal(t, tc.agentName, ag.Name)
			assert.NotEmpty(t, ag.ID)
			assert.True(t, len(ag.ID) > 0 && ag.ID != ag.Name, "ID should be auto-generated, not the name")
		})
	}
}

// IT-52: Creating Agent without MCP Servers uses default MCP servers.
func TestAgentCreate_DefaultMCP(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Create agent without specifying mcp_servers
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "default-mcp-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Should have default MCP servers (core)
	require.NotEmpty(t, ag.Config.MCPServers, "should have default MCP servers")
	assert.Equal(t, "core", ag.Config.MCPServers[0].Name)
}

// IT-53: Creating Agent without system_prompt still generates valid SYSTEM.md.
func TestAgentCreate_NoSystemPrompt(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "no-prompt-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Agent should be running with default model
	assert.Equal(t, agentpkg.StatusRunning, ag.Status)
	assert.Equal(t, "test-model", ag.Config.Model)

	// System prompt should be accessible
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/system-prompt", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var promptResp struct {
		SystemPrompt string `json:"system_prompt"`
	}
	decodeJSON(t, resp, &promptResp)
	assert.NotEmpty(t, promptResp.SystemPrompt, "should have a default system prompt")
}

// IT-54: Creating Agent with only name and model (minimal frontend request).
func TestAgentCreate_MinimalRequest(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name":  "简单助手",
		"model": "test-model",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	assert.Equal(t, "简单助手", ag.Name)
	assert.Equal(t, "test-model", ag.Config.Model)
	assert.Equal(t, agentpkg.StatusRunning, ag.Status)
	assert.NotEmpty(t, ag.Config.MCPServers, "should have default MCP servers")
}
