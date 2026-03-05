//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-09: Agent Loop — Mock LLM returns tool_call → MCP executes → result feeds back → LLM returns final text
func TestAgentLoop(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	// Setup MCP pool and router
	ctx := context.Background()
	pool := mcp.NewPool()
	err := pool.Connect(ctx, dockerMgr, containerID, []mcp.MCPServerDef{
		{Name: "core", Command: "mcp-server-core"},
	})
	require.NoError(t, err)
	defer pool.CloseAll()

	router := mcp.NewRouter(pool)

	// Setup Mock LLM: first call returns tool_call, second call returns final text
	mockServer := NewMockLLMServer([]MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "shell_exec",
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
	finalContent, messages, err := agent.RunLoop(loopCfg, history, func(event agent.StreamEvent) {
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
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	pool := mcp.NewPool()
	err := pool.Connect(ctx, dockerMgr, containerID, []mcp.MCPServerDef{
		{Name: "core", Command: "mcp-server-core"},
	})
	require.NoError(t, err)
	defer pool.CloseAll()

	router := mcp.NewRouter(pool)

	// Mock LLM: first writes a file, then reads it, then returns final text
	mockServer := NewMockLLMServer([]MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "file_write",
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
						Name:      "file_read",
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

	finalContent, _, err := agent.RunLoop(loopCfg, history, nil)
	require.NoError(t, err)
	assert.Contains(t, finalContent, "loop test data")
	assert.Equal(t, 3, mockServer.CallCount)
}
