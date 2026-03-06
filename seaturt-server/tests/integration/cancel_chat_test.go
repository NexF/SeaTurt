//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCancelChat verifies the cancel chat flow:
//  1. Create agent + send chat that triggers a slow tool call (sleep 30)
//  2. While the tool call is running, call POST /chat/cancel
//  3. Verify SSE stream contains a "cancelled" event
//  4. Verify messages were persisted to DB (user + assistant with tool_call at minimum)
func TestCancelChat(t *testing.T) {
	t.Parallel()

	// Mock LLM: first call returns a slow shell command, second would return final text
	// (but we'll cancel before the second call happens)
	mockResponses := []MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_slow",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "shell_exec",
						Arguments: `{"command":"sleep 30"}`,
					},
				},
			},
		},
		{
			Content: "This should not be reached due to cancellation",
		},
	}

	ts, _ := newTestServer(t, mockResponses)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "cancel-chat-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created agentpkg.Agent
	decodeJSON(t, resp, &created)
	agentID := created.ID

	// Send chat message (SSE stream)
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "run a slow command"}},
	})
	chatReq, err := http.NewRequest("POST", ts.URL+"/api/agents/"+agentID+"/chat", bytes.NewReader(chatBody))
	require.NoError(t, err)
	chatReq.Header.Set("Content-Type", "application/json")

	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Collect SSE events in a goroutine
	var events []agentpkg.StreamEvent
	var mu sync.Mutex
	streamDone := make(chan struct{})

	go func() {
		defer close(streamDone)
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
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		}
	}()

	// Wait for the tool_call event to appear (indicating the tool is executing)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			if e.Type == "tool_call" {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "tool_call event should appear")

	// Small delay to ensure the tool is actually executing
	time.Sleep(500 * time.Millisecond)

	// Call cancel API
	cancelResp := doRequest(t, ts, "POST", "/api/agents/"+agentID+"/chat/cancel", nil)
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)

	var cancelResult map[string]string
	decodeJSON(t, cancelResp, &cancelResult)
	assert.Equal(t, "cancelled", cancelResult["status"])

	// Wait for SSE stream to finish
	select {
	case <-streamDone:
	case <-time.After(10 * time.Second):
		t.Fatal("SSE stream did not close after cancel")
	}

	// Verify we got a "cancelled" event
	mu.Lock()
	eventTypes := make(map[string]int)
	for _, e := range events {
		eventTypes[e.Type]++
	}
	mu.Unlock()

	assert.Greater(t, eventTypes["tool_call"], 0, "should have tool_call event")
	assert.Greater(t, eventTypes["cancelled"], 0, "should have cancelled event")
	assert.Equal(t, 0, eventTypes["done"], "should NOT have done event (was cancelled)")

	// Verify messages were persisted
	histResp := doRequest(t, ts, "GET", "/api/agents/"+agentID+"/history", nil)
	assert.Equal(t, http.StatusOK, histResp.StatusCode)

	var messages []agentpkg.Message
	decodeJSON(t, histResp, &messages)
	// At minimum: user message + assistant message (with tool_calls)
	assert.GreaterOrEqual(t, len(messages), 2, "should have persisted at least user + assistant messages")

	// The first message should be the user message
	assert.Equal(t, "user", messages[0].Role)

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+agentID, nil)
}

// TestCancelChat_NoActiveSession verifies calling cancel when no chat is running.
func TestCancelChat_NoActiveSession(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t, nil)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "cancel-no-session",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created agentpkg.Agent
	decodeJSON(t, resp, &created)

	// Call cancel — should return no_active_session
	cancelResp := doRequest(t, ts, "POST", "/api/agents/"+created.ID+"/chat/cancel", nil)
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)

	var result map[string]string
	decodeJSON(t, cancelResp, &result)
	assert.Equal(t, "no_active_session", result["status"])

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+created.ID, nil)
}
