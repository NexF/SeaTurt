//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/api"
	"github.com/seaturt/server/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newE2ETestServer creates a full API server stack with the real LLM client.
func newE2ETestServer(t *testing.T) *httptest.Server {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "e2e-api-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	tmpDB.Close()

	wsRoot, err := os.MkdirTemp(testWorkspace, "e2e-ws-*")
	require.NoError(t, err)

	s, err := store.New(dbPath)
	require.NoError(t, err)

	cfg := *testCfg
	cfg.WorkspaceRoot = wsRoot

	agentMgr := agentpkg.NewManager(&cfg, s, dockerMgr, llmClient)
	server := api.NewServer(0, agentMgr, cfg.MaxImageSize)

	ts := httptest.NewServer(server)

	t.Cleanup(func() {
		ts.Close()
		// Cleanup agents
		agents, _ := agentMgr.List()
		for _, ag := range agents {
			_ = agentMgr.Delete(t.Context(), ag.ID)
		}
		s.Close()
		os.Remove(dbPath)
		os.RemoveAll(wsRoot)
	})

	return ts
}

func doE2ERequest(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
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

func decodeE2EJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	err := json.NewDecoder(resp.Body).Decode(target)
	require.NoError(t, err)
}

// parseE2ESSEEvents parses SSE events from a response, supporting both "data: " and "data:" formats.
func parseE2ESSEEvents(t *testing.T, resp *http.Response) []agentpkg.StreamEvent {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
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
	return events
}

// E2E-01: Basic chat with real LLM — English
func TestE2E_BasicChat(t *testing.T) {
	ts := newE2ETestServer(t)

	// Create agent
	resp := doE2ERequest(t, ts, "POST", "/api/agents", map[string]any{"name": "e2e-basic"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeE2EJSON(t, resp, &ag)

	// Send English message
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "Reply with exactly: E2E_OK"}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Verify Content-Type includes charset=utf-8
	ct := chatResp.Header.Get("Content-Type")
	assert.Contains(t, ct, "text/event-stream", "Content-Type should be text/event-stream")
	assert.Contains(t, ct, "charset=utf-8", "Content-Type should include charset=utf-8")

	// Parse SSE events
	events := parseE2ESSEEvents(t, chatResp)
	types := make(map[string]bool)
	for _, e := range events {
		types[e.Type] = true
	}

	assert.True(t, types["text_delta"] || types["done"], "should have text_delta or done event")
	assert.True(t, types["done"], "should have done event")

	// Cleanup
	doE2ERequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// E2E-02: Chat with Chinese content — validates UTF-8 encoding in SSE
func TestE2E_ChineseChat(t *testing.T) {
	ts := newE2ETestServer(t)

	// Create agent
	resp := doE2ERequest(t, ts, "POST", "/api/agents", map[string]any{"name": "e2e-chinese"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeE2EJSON(t, resp, &ag)

	// Send Chinese message
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "请用中文回复：你好"}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Verify charset
	assert.Contains(t, chatResp.Header.Get("Content-Type"), "charset=utf-8")

	// Parse SSE events and verify Chinese content is not garbled
	var fullText strings.Builder
	scanner := bufio.NewScanner(chatResp.Body)
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
		if event.Type == "text_delta" {
			if m, ok := event.Data.(map[string]any); ok {
				if content, ok := m["content"].(string); ok {
					fullText.WriteString(content)
				}
			}
		}
	}

	text := fullText.String()
	t.Logf("LLM response: %s", text)

	// The response should be valid UTF-8 and contain some Chinese characters
	assert.True(t, len(text) > 0, "response should not be empty")
	// Basic check: response should not contain mojibake patterns (ISO-8859-1 decoded UTF-8)
	assert.NotContains(t, text, "ä½", "response should not contain mojibake")
	assert.NotContains(t, text, "å¥½", "response should not contain mojibake")

	// Cleanup
	doE2ERequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// E2E-03: Chat with tool execution — real LLM should call shell_exec
func TestE2E_ToolExecution(t *testing.T) {
	ts := newE2ETestServer(t)

	// Create agent
	resp := doE2ERequest(t, ts, "POST", "/api/agents", map[string]any{"name": "e2e-tool"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeE2EJSON(t, resp, &ag)

	// Ask LLM to run a simple command
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "Please run the command: echo E2E_TOOL_TEST and tell me the output."}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Parse events
	events := parseE2ESSEEvents(t, chatResp)
	types := make(map[string]bool)
	for _, e := range events {
		types[e.Type] = true
	}

	// Real LLM should attempt to use tools
	assert.True(t, types["tool_call"], "real LLM should attempt tool calls")
	assert.True(t, types["tool_result"], "should have tool results")
	assert.True(t, types["done"], "should complete successfully")

	// Verify history was saved
	resp = doE2ERequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var messages []json.RawMessage
	decodeE2EJSON(t, resp, &messages)
	assert.GreaterOrEqual(t, len(messages), 2, "should have at least user + assistant messages")

	// Cleanup
	doE2ERequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// E2E-04: SSE format validation — verifies the raw SSE bytes
func TestE2E_SSEFormatValidation(t *testing.T) {
	ts := newE2ETestServer(t)

	resp := doE2ERequest(t, ts, "POST", "/api/agents", map[string]any{"name": "e2e-sse-format"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeE2EJSON(t, resp, &ag)

	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "Say OK"}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	// Verify response headers
	assert.Equal(t, http.StatusOK, chatResp.StatusCode)
	ct := chatResp.Header.Get("Content-Type")
	assert.Contains(t, ct, "text/event-stream")
	assert.Contains(t, ct, "charset=utf-8")
	assert.Equal(t, "no-cache", chatResp.Header.Get("Cache-Control"))

	// Read raw lines and verify SSE format
	scanner := bufio.NewScanner(chatResp.Body)
	var dataLineCount int
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataLineCount++
			// Each data line should be valid JSON or [DONE]
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			if payload == "[DONE]" {
				continue
			}
			var raw json.RawMessage
			err := json.Unmarshal([]byte(payload), &raw)
			assert.NoError(t, err, "SSE data line should contain valid JSON: %s", payload)
		}
	}
	assert.Greater(t, dataLineCount, 0, "should have at least one SSE data line")

	// Cleanup
	doE2ERequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}
