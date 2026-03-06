//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	_ "github.com/seaturt/server/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-39: After creating an Agent, .seaturt/SYSTEM.md should exist with correct content.
func TestWorkspaceFiles_SystemMD(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Create agent (default, no custom system_prompt)
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "system-md-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Verify SYSTEM.md exists
	systemMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	data, err := os.ReadFile(systemMDPath)
	require.NoError(t, err, "SYSTEM.md should exist")

	content := string(data)
	assert.Contains(t, content, "# Agent 系统指令")
	assert.Contains(t, content, "## 身份")
	assert.Contains(t, content, "## 行为准则")
	assert.Contains(t, content, "/workspace")
	assert.Contains(t, content, "## 可用工具")
	assert.Contains(t, content, "core") // default MCP server
}

// IT-40: After creating an Agent, .seaturt/PORTS.md should exist with port mappings.
func TestWorkspaceFiles_PortsMD(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "ports-md-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// PORTS.md may or may not exist depending on whether the sandbox image exposes ports.
	// If it exists, it should have the correct format.
	portsMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "PORTS.md")
	data, err := os.ReadFile(portsMDPath)
	if err == nil {
		content := string(data)
		assert.Contains(t, content, "# 端口映射")
		assert.Contains(t, content, "| 容器端口 | 宿主机端口 | 用途 |")
	}
	// If no ports are exposed, PORTS.md not existing is also acceptable
}

// IT-41: Without specifying system_prompt, SYSTEM.md uses default template.
func TestSystemPrompt_Default(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "default-prompt-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	systemMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	data, err := os.ReadFile(systemMDPath)
	require.NoError(t, err)

	content := string(data)
	// Should have default sections, not "附加指令"
	assert.Contains(t, content, "## 身份")
	assert.NotContains(t, content, "## 附加指令")
	// Unified image: all agents now include desktop
	assert.Contains(t, content, "## 桌面环境")
}

// IT-42: When specifying system_prompt, it should appear in the "附加指令" section.
func TestSystemPrompt_Custom(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	customPrompt := "你是一个数据分析专家，擅长 Python 和 SQL。"
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name":          "custom-prompt-test",
		"system_prompt": customPrompt,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	systemMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	data, err := os.ReadFile(systemMDPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "## 附加指令")
	assert.Contains(t, content, customPrompt)
}

// IT-43: All agents have desktop capabilities (unified image), SYSTEM.md should include desktop-related instructions.
func TestSystemPrompt_Desktop(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// All agents now include desktop MCP server by default (unified image)
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "desktop-prompt-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	systemMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	data, err := os.ReadFile(systemMDPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "## 桌面环境")
	assert.Contains(t, content, "screenshot")
	assert.Contains(t, content, "mouse_click")
	assert.Contains(t, content, "VNC")
}

// IT-44: During Chat, the LLM should receive the system message from SYSTEM.md.
func TestSystemPrompt_UsedInLoop(t *testing.T) {
	t.Parallel()
	// Mock LLM that captures the system prompt from the request
	var capturedMessages json.RawMessage

	mockResponses := []MockLLMResponse{
		{Content: "Hello from agent"},
	}
	ts, mockLLM := newTestServer(t, mockResponses)

	// Intercept the mock LLM to capture the request body
	mockLLM.SetRequestCapture(func(body []byte) {
		var req struct {
			Messages json.RawMessage `json:"messages"`
		}
		json.Unmarshal(body, &req)
		capturedMessages = req.Messages
	})

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name":          "loop-prompt-test",
		"system_prompt": "你是测试专用 Agent",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Send chat message
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "hello"}},
	})
	chatReq, err := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	require.NoError(t, err)
	chatReq.Header.Set("Content-Type", "application/json")

	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()
	require.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Consume SSE body fully
	scanner := bufio.NewScanner(chatResp.Body)
	for scanner.Scan() {
		// drain
	}

	// Verify the captured messages contain our system prompt from SYSTEM.md
	require.NotNil(t, capturedMessages, "should have captured LLM request messages")

	var msgs []map[string]any
	err = json.Unmarshal(capturedMessages, &msgs)
	require.NoError(t, err)
	require.Greater(t, len(msgs), 0, "should have at least one message")

	// First message should be system role with SYSTEM.md content
	assert.Equal(t, "system", msgs[0]["role"])

	// The system message content should contain our custom prompt (embedded in SYSTEM.md)
	systemContent := fmt.Sprintf("%v", msgs[0]["content"])
	assert.Contains(t, systemContent, "Agent 系统指令", "should contain SYSTEM.md content")
	assert.Contains(t, systemContent, "你是测试专用 Agent", "should contain custom system prompt")
}

// IT-45: Modifying SYSTEM.md should take effect on the next Chat call.
func TestSystemPrompt_HotReload(t *testing.T) {
	t.Parallel()
	var capturedSystemPrompt string

	mockResponses := []MockLLMResponse{
		{Content: "First response"},
		{Content: "Second response"},
	}
	ts, mockLLM := newTestServer(t, mockResponses)

	// Capture system message for each request
	var captureCount int
	mockLLM.SetRequestCapture(func(body []byte) {
		captureCount++
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(body, &req)
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			capturedSystemPrompt = string(req.Messages[0].Content)
		}
	})

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "hot-reload-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// First Chat — uses original SYSTEM.md
	sendChat(t, ts, ag.ID, "first message")
	assert.NotContains(t, capturedSystemPrompt, "热更新测试内容")

	// Overwrite SYSTEM.md via PUT API
	newPrompt := "# 热更新测试内容\n\n这是新的 system prompt。"
	updateReq, _ := http.NewRequest("PUT", ts.URL+"/api/agents/"+ag.ID+"/system-prompt", strings.NewReader(newPrompt))
	updateResp, err := http.DefaultClient.Do(updateReq)
	require.NoError(t, err)
	updateResp.Body.Close()
	assert.Equal(t, http.StatusOK, updateResp.StatusCode)

	// Clear history to avoid history contamination
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID+"/history", nil)

	// Second Chat — should use updated SYSTEM.md
	sendChat(t, ts, ag.ID, "second message")
	assert.Contains(t, capturedSystemPrompt, "热更新测试内容")
}

// IT-46: Deleting SYSTEM.md should fallback to DefaultSystemPrompt.
func TestSystemPrompt_Fallback(t *testing.T) {
	t.Parallel()
	var capturedSystemPrompt string

	mockResponses := []MockLLMResponse{
		{Content: "Fallback response"},
	}
	ts, mockLLM := newTestServer(t, mockResponses)

	mockLLM.SetRequestCapture(func(body []byte) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(body, &req)
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			capturedSystemPrompt = string(req.Messages[0].Content)
		}
	})

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "fallback-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Delete SYSTEM.md
	systemMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	err := os.Remove(systemMDPath)
	require.NoError(t, err)

	// Chat — should use DefaultSystemPrompt
	sendChat(t, ts, ag.ID, "hello after delete")

	// DefaultSystemPrompt content
	assert.Contains(t, capturedSystemPrompt, "helpful coding assistant")
}

// IT-47: After Stop/Start, PORTS.md should be regenerated.
func TestPortsMD_Regenerated(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "ports-regen-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	portsMDPath := filepath.Join(ag.WorkspacePath, ".seaturt", "PORTS.md")

	// Read original PORTS.md (if exists)
	originalData, originalExists := readFileIfExists(portsMDPath)

	// Delete PORTS.md
	_ = os.Remove(portsMDPath)

	// Stop then Start
	resp = doRequest(t, ts, "POST", "/api/agents/"+ag.ID+"/stop", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = doRequest(t, ts, "POST", "/api/agents/"+ag.ID+"/start", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check PORTS.md is regenerated
	newData, newExists := readFileIfExists(portsMDPath)

	if originalExists {
		// If the image exposes ports, PORTS.md should be regenerated
		require.True(t, newExists, "PORTS.md should be regenerated after restart")
		assert.Contains(t, string(newData), "# 端口映射")
		_ = originalData // both should have port mapping content
	}
	// If no ports are exposed, both may be absent — that's fine
}

// IT-48: GET /api/agents/:id/ports should return correct port mapping.
func TestPortsAPI(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "ports-api-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// GET /api/agents/:id/ports
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/ports", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var portsResp struct {
		Ports map[string]struct {
			HostPort    string `json:"host_port"`
			Description string `json:"description"`
		} `json:"ports"`
	}
	decodeJSON(t, resp, &portsResp)

	// Ports map should be a valid map (possibly empty if no ports exposed)
	require.NotNil(t, portsResp.Ports)

	// If the container exposes ports, verify the structure
	for containerPort, info := range portsResp.Ports {
		assert.NotEmpty(t, containerPort)
		assert.NotEmpty(t, info.HostPort)
		assert.NotEmpty(t, info.Description)
	}
}

// --- Helpers ---

// sendChat sends a chat message and drains the SSE response.
func sendChat(t *testing.T, ts *httptest.Server, agentID, text string) {
	t.Helper()

	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	chatReq, err := http.NewRequest("POST", ts.URL+"/api/agents/"+agentID+"/chat", bytes.NewReader(chatBody))
	require.NoError(t, err)
	chatReq.Header.Set("Content-Type", "application/json")

	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()
	require.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Drain SSE
	scanner := bufio.NewScanner(chatResp.Body)
	for scanner.Scan() {
		// drain
	}
}

// readFileIfExists reads a file, returning (data, true) if it exists or (nil, false) otherwise.
func readFileIfExists(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}
