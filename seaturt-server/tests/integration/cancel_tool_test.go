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

// TestCancelToolCall verifies the per-tool-call cancellation flow:
//  1. Create agent + send chat that triggers a slow tool call (sleep 30)
//  2. While the tool call is running, call POST /chat/cancel-tool/:toolCallId
//  3. Verify SSE stream contains a tool_result with "用户取消了此工具调用"
//  4. Verify the agent continues reasoning (receives the second LLM response with final text)
//  5. Verify messages were persisted including the cancelled tool result
func TestCancelToolCall(t *testing.T) {
	t.Parallel()

	// Mock LLM: first call returns a slow shell command,
	// second call returns final text (agent continues after cancellation)
	mockResponses := []MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_slow_tool",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "shell_exec",
						Arguments: `{"command":"sleep 30"}`,
					},
				},
			},
		},
		{
			Content: "The tool call was cancelled by the user. Let me try a different approach.",
		},
	}

	ts, mockLLM := newTestServer(t, mockResponses)
	_ = mockLLM

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "cancel-tool-test",
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

	// Wait for the tool_call event to appear
	var toolCallID string
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range events {
			if e.Type == "tool_call" {
				// Extract the tool call ID from the event data
				if dataMap, ok := e.Data.(map[string]any); ok {
					if id, ok := dataMap["id"].(string); ok {
						toolCallID = id
						return true
					}
				}
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "tool_call event should appear")

	require.NotEmpty(t, toolCallID, "should have captured tool call ID")

	// Small delay to ensure the tool is actually executing
	time.Sleep(500 * time.Millisecond)

	// Call cancel-tool API for this specific tool call
	cancelResp := doRequest(t, ts, "POST", "/api/agents/"+agentID+"/chat/cancel-tool/"+toolCallID, nil)
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)

	var cancelResult map[string]string
	decodeJSON(t, cancelResp, &cancelResult)
	assert.Equal(t, "cancelled", cancelResult["status"])

	// Wait for SSE stream to finish (agent should continue reasoning and eventually emit "done")
	select {
	case <-streamDone:
	case <-time.After(30 * time.Second):
		t.Fatal("SSE stream did not close after tool cancellation + continued reasoning")
	}

	// Verify events
	mu.Lock()
	eventTypes := make(map[string]int)
	var toolResults []agentpkg.StreamEvent
	for _, e := range events {
		eventTypes[e.Type]++
		if e.Type == "tool_result" {
			toolResults = append(toolResults, e)
		}
	}
	mu.Unlock()

	// Should have: tool_call, tool_result (cancelled), text_delta (from second LLM response), done
	assert.Greater(t, eventTypes["tool_call"], 0, "should have tool_call event")
	assert.Greater(t, eventTypes["tool_result"], 0, "should have tool_result event (cancelled)")
	assert.Equal(t, 1, eventTypes["done"], "should have done event (agent continued)")
	assert.Equal(t, 0, eventTypes["cancelled"], "should NOT have cancelled event (only tool was cancelled)")

	// Verify the tool_result contains cancellation message
	require.NotEmpty(t, toolResults)
	trData, ok := toolResults[0].Data.(map[string]any)
	require.True(t, ok, "tool_result data should be a map")
	assert.Equal(t, toolCallID, trData["tool_call_id"])
	assert.Equal(t, true, trData["is_error"])

	// Check content contains cancellation text
	if content, ok := trData["content"].([]any); ok && len(content) > 0 {
		if block, ok := content[0].(map[string]any); ok {
			assert.Contains(t, block["text"], "取消")
		}
	}

	// Verify messages were persisted
	histResp := doRequest(t, ts, "GET", "/api/agents/"+agentID+"/history", nil)
	assert.Equal(t, http.StatusOK, histResp.StatusCode)

	var messages []agentpkg.Message
	decodeJSON(t, histResp, &messages)
	// Expected: user + assistant(tool_call) + tool(cancelled result) + assistant(final)
	assert.GreaterOrEqual(t, len(messages), 4,
		"should have user + assistant(tool_call) + tool(cancelled) + assistant(final)")

	// Verify there's a tool message with cancellation content
	foundCancelledTool := false
	for _, msg := range messages {
		if msg.Role == "tool" {
			contentStr := msg.Content.String()
			if strings.Contains(contentStr, "取消") {
				foundCancelledTool = true
				break
			}
		}
	}
	assert.True(t, foundCancelledTool, "should have a tool message with cancellation content")

	// Verify the mock LLM was called twice (tool_call round + final after cancel)
	assert.Equal(t, 2, mockLLM.CallCount, "LLM should have been called twice (tool_call + final after cancel)")

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+agentID, nil)
}

// TestCancelToolCall_NotFound verifies calling cancel-tool with non-existent tool call ID.
func TestCancelToolCall_NotFound(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t, nil)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "cancel-tool-notfound",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created agentpkg.Agent
	decodeJSON(t, resp, &created)

	// Call cancel-tool with non-existent ID
	cancelResp := doRequest(t, ts, "POST", "/api/agents/"+created.ID+"/chat/cancel-tool/nonexistent_id", nil)
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)

	var result map[string]string
	decodeJSON(t, cancelResp, &result)
	assert.Equal(t, "tool_call_not_found", result["status"])

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+created.ID, nil)
}
