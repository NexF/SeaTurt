//go:build integration

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-09: Agent Loop — Mock LLM returns tool_call → MCP executes → result feeds back → LLM returns final text
func TestAgentLoop(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)

	// Setup Mock LLM: first call returns tool_call, second call returns final text
	mockServer := NewMockLLMServer([]MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "core-shell_exec",
						Arguments: `{"command":"echo integration_test_ok"}`,
					},
				},
			},
		},
		{
			Content: "The command executed successfully. Output: integration_test_ok",
		},
	})
	defer mockServer.Close()

	llmClient := mockServer.NewClient()

	// Run Agent Loop
	loopCfg := agent.LoopConfig{
		LLMClient: llmClient,
		Router:    router,
	}

	history := []llm.ChatMessage{
		{Role: "user", Content: llm.Content{llm.NewTextContent("Run echo integration_test_ok")}},
	}

	var events []agent.StreamEvent
	finalContent, messages, err := agent.RunLoop(context.Background(), loopCfg, history, func(event agent.StreamEvent) {
		events = append(events, event)
	})
	require.NoError(t, err)

	// Verify final content
	assert.Contains(t, finalContent, "integration_test_ok")

	// Verify LLM was called twice (tool_call + final)
	assert.Equal(t, 2, mockServer.CallCount)

	// Verify message history includes system + user + assistant(tool_call) + tool + assistant(final)
	assert.GreaterOrEqual(t, len(messages), 5)

	// Verify events were emitted
	eventTypes := make(map[string]int)
	for _, e := range events {
		eventTypes[e.Type]++
	}
	assert.Greater(t, eventTypes["tool_call"], 0, "should have tool_call events")
	assert.Greater(t, eventTypes["tool_result"], 0, "should have tool_result events")
	assert.Equal(t, 1, eventTypes["done"], "should have exactly one done event")
}

// IT-09b: Agent Loop with multiple tool calls in sequence
func TestAgentLoopMultipleTools(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)

	// Mock LLM: first writes a file, then reads it, then returns final text
	mockServer := NewMockLLMServer([]MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "core-file_write",
						Arguments: `{"path":"/workspace/loop_test.txt","content":"loop test data"}`,
					},
				},
			},
		},
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_2",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "core-file_read",
						Arguments: `{"path":"/workspace/loop_test.txt"}`,
					},
				},
			},
		},
		{
			Content: "File contains: loop test data",
		},
	})
	defer mockServer.Close()

	llmClient := mockServer.NewClient()

	loopCfg := agent.LoopConfig{
		LLMClient: llmClient,
		Router:    router,
	}

	history := []llm.ChatMessage{
		{Role: "user", Content: llm.Content{llm.NewTextContent("Write and then read a file")}},
	}

	finalContent, _, err := agent.RunLoop(context.Background(), loopCfg, history, nil)
	require.NoError(t, err)
	assert.Contains(t, finalContent, "loop test data")
	assert.Equal(t, 3, mockServer.CallCount)
}

// TestAgentLoop_SingleToolCallCancel verifies that:
//  1. When a single tool call's context is cancelled (via OnToolCallStart cancel func),
//     the RunLoop continues reasoning instead of aborting the entire chat.
//  2. The OnMessage callback is invoked for every message produced (assistant + tool results).
//  3. The cancelled tool returns "用户取消了此工具调用" as its result.
//  4. The LLM receives the cancellation result and produces a final text response.
func TestAgentLoop_SingleToolCallCancel(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)

	// Mock LLM:
	//   1st call → returns tool_call for "sleep 30" (slow command)
	//   2nd call → receives cancelled tool result, returns final text
	mockServer := NewMockLLMServer([]MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_cancel_me",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "core-shell_exec",
						Arguments: `{"command":"sleep 30"}`,
					},
				},
			},
		},
		{
			Content: "Tool was cancelled, continuing with alternative approach.",
		},
	})
	defer mockServer.Close()

	llmClient := mockServer.NewClient()

	// Track OnMessage calls
	var onMessageCalls []llm.ChatMessage
	var mu sync.Mutex

	// Track tool call registrations
	var toolCallCancels sync.Map // toolCallID -> context.CancelFunc

	loopCfg := agent.LoopConfig{
		LLMClient: llmClient,
		Router:    router,
		OnMessage: func(msg llm.ChatMessage) {
			mu.Lock()
			onMessageCalls = append(onMessageCalls, msg)
			mu.Unlock()
		},
		OnToolCallStart: func(toolCallID string, cancel context.CancelFunc) {
			toolCallCancels.Store(toolCallID, cancel)
			// Cancel the tool call after a short delay (simulating user clicking cancel)
			go func() {
				time.Sleep(500 * time.Millisecond)
				cancel()
			}()
		},
		OnToolCallEnd: func(toolCallID string) {
			toolCallCancels.Delete(toolCallID)
		},
	}

	history := []llm.ChatMessage{
		{Role: "user", Content: llm.Content{llm.NewTextContent("Run a slow command")}},
	}

	var events []agent.StreamEvent
	finalContent, messages, err := agent.RunLoop(context.Background(), loopCfg, history, func(event agent.StreamEvent) {
		events = append(events, event)
	})
	require.NoError(t, err, "RunLoop should succeed (single tool cancel doesn't abort chat)")

	// Verify final content came from the second LLM response
	assert.Contains(t, finalContent, "cancelled")

	// Verify LLM was called twice
	assert.Equal(t, 2, mockServer.CallCount, "LLM should be called twice: tool_call round + final after cancel")

	// Verify messages include the cancelled tool result
	foundCancelledResult := false
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_cancel_me" {
			text := msg.Content.String()
			if strings.Contains(text, "取消") {
				foundCancelledResult = true
			}
		}
	}
	assert.True(t, foundCancelledResult, "should have a tool message with cancellation text")

	// Verify OnMessage was called for each produced message
	mu.Lock()
	msgCount := len(onMessageCalls)
	// Expected: assistant(tool_call) + tool(cancelled) + assistant(final) = 3
	assert.GreaterOrEqual(t, msgCount, 3, "OnMessage should be called at least 3 times")

	// Verify roles in OnMessage calls
	roles := make([]string, len(onMessageCalls))
	for i, m := range onMessageCalls {
		roles[i] = m.Role
	}
	mu.Unlock()

	// Should have: assistant, tool, assistant (in that order)
	assert.Contains(t, roles, "assistant")
	assert.Contains(t, roles, "tool")

	// Verify events
	eventTypes := make(map[string]int)
	for _, e := range events {
		eventTypes[e.Type]++
	}
	assert.Greater(t, eventTypes["tool_call"], 0, "should have tool_call event")
	assert.Greater(t, eventTypes["tool_result"], 0, "should have tool_result event")
	assert.Equal(t, 1, eventTypes["done"], "should have done event (not cancelled)")
	assert.Equal(t, 0, eventTypes["cancelled"], "should NOT have cancelled event")
}
