package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"
)

const (
	// MaxToolOutputLen is the maximum character length for a single tool output.
	// Outputs exceeding this limit will be truncated to prevent blowing up LLM context.
	MaxToolOutputLen = 50_000

	// MaxLoopIterations prevents infinite tool-call loops.
	MaxLoopIterations = 50

	// DefaultSystemPrompt is the fallback system prompt when SYSTEM.md is not available.
	DefaultSystemPrompt = `You are a helpful coding assistant running inside a sandboxed container.
You have access to tools that let you execute shell commands, read/write files, and more.
Always prefer using tools to answer questions when appropriate.
Be concise and precise in your responses.`
)

// LoopConfig holds the configuration for an agent loop execution.
type LoopConfig struct {
	LLMClient    *llm.Client
	Router       *mcp.Router
	SystemPrompt string // if non-empty, use this prompt; otherwise fallback to DefaultSystemPrompt
}

// StreamEvent represents an event emitted during the agent loop for SSE streaming.
type StreamEvent struct {
	Type string `json:"type"` // "text_delta", "tool_call", "tool_result", "error", "done"
	Data any    `json:"data"`
}

// TextDelta is the data for a "text_delta" event.
type TextDelta struct {
	Content string `json:"content"`
}

// ToolCallEvent is the data for a "tool_call" event.
type ToolCallEvent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResultEvent is the data for a "tool_result" event.
type ToolResultEvent struct {
	ToolCallID string             `json:"tool_call_id"`
	Content    []llm.ContentBlock `json:"content"`
	IsError    bool               `json:"is_error"`
}

// StreamFunc is called for each event during the agent loop.
type StreamFunc func(event StreamEvent)

// RunLoop executes the Agent Loop:
//  1. Collects all tools from the MCP Router
//  2. Sends messages + tools to the LLM
//  3. If LLM returns tool_calls, executes them via the Router
//  4. Feeds results back to LLM and repeats
//  5. Stops when LLM returns final text (no tool_calls) or max iterations reached
//
// The streamFn is called for each event (text delta, tool call, tool result).
// It may be nil for non-streaming use.
// Returns the full assistant response and the updated message history.
func RunLoop(cfg LoopConfig, history []llm.ChatMessage, streamFn StreamFunc) (string, []llm.ChatMessage, error) {
	// Collect all tools from the router and convert to OpenAI format
	mcpTools := cfg.Router.AllTools()
	toolDefs := llm.ConvertMCPTools(mcpTools)

	slog.Info("agent loop starting",
		"tools", len(toolDefs),
		"history_len", len(history),
	)

	// Ensure system prompt is present
	prompt := cfg.SystemPrompt
	if prompt == "" {
		prompt = DefaultSystemPrompt
	}
	if len(history) == 0 || history[0].Role != "system" {
		history = append([]llm.ChatMessage{{
			Role:    "system",
			Content: llm.Content{llm.NewTextContent(prompt)},
		}}, history...)
	}

	messages := make([]llm.ChatMessage, len(history))
	copy(messages, history)

	var finalContent string

	for i := 0; i < MaxLoopIterations; i++ {
		slog.Debug("agent loop iteration", "i", i)

		var resp *llm.ChatResponse
		var err error

		if streamFn != nil {
			resp, err = cfg.LLMClient.ChatCompletionStream(messages, toolDefs, func(delta llm.StreamDelta) error {
				for _, choice := range delta.Choices {
					if choice.Delta != nil {
						if text := choice.Delta.Content.String(); text != "" {
							streamFn(StreamEvent{
								Type: "text_delta",
								Data: TextDelta{Content: text},
							})
						}
					}
				}
				return nil
			})
		} else {
			resp, err = cfg.LLMClient.ChatCompletion(messages, toolDefs)
		}

		if err != nil {
			if streamFn != nil {
				streamFn(StreamEvent{Type: "error", Data: map[string]string{"message": err.Error()}})
			}
			return "", messages, fmt.Errorf("LLM call failed (iteration %d): %w", i, err)
		}

		if len(resp.Choices) == 0 {
			return "", messages, fmt.Errorf("LLM returned no choices (iteration %d)", i)
		}

		assistantMsg := resp.Choices[0].Message

		// Append assistant message to history
		messages = append(messages, assistantMsg)

		// No tool calls → final response
		if len(assistantMsg.ToolCalls) == 0 {
			finalContent = assistantMsg.Content.String()
			if streamFn != nil {
				streamFn(StreamEvent{Type: "done", Data: nil})
			}
			slog.Info("agent loop completed", "iterations", i+1)
			return finalContent, messages, nil
		}

		// Process tool calls
		for _, tc := range assistantMsg.ToolCalls {
			if streamFn != nil {
				streamFn(StreamEvent{
					Type: "tool_call",
					Data: ToolCallEvent{
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}

			slog.Debug("executing tool call",
				"id", tc.ID,
				"tool", tc.Function.Name,
			)

			// Parse arguments
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					toolResult := fmt.Sprintf("Error parsing arguments: %s", err.Error())
					toolResultBlocks := []llm.ContentBlock{llm.NewTextContent(toolResult)}
					messages = append(messages, llm.ChatMessage{
						Role:       "tool",
						Content:    llm.Content(toolResultBlocks),
						ToolCallID: tc.ID,
					})
					if streamFn != nil {
						streamFn(StreamEvent{
							Type: "tool_result",
							Data: ToolResultEvent{ToolCallID: tc.ID, Content: toolResultBlocks, IsError: true},
						})
					}
					continue
				}
			}

			// Execute via router
			result, err := cfg.Router.Route(tc.Function.Name, args)
			var toolContent llm.Content
			var isError bool

			if err != nil {
				toolContent = llm.Content{llm.NewTextContent(fmt.Sprintf("Error: %s", err.Error()))}
				isError = true
			} else {
				toolContent = formatToolResult(result)
				isError = result.IsError
			}

			// Truncation protection (only text blocks)
			toolContent = truncateContentBlocks(toolContent, MaxToolOutputLen)

			messages = append(messages, llm.ChatMessage{
				Role:       "tool",
				Content:    toolContent,
				ToolCallID: tc.ID,
			})

			if streamFn != nil {
				streamFn(StreamEvent{
					Type: "tool_result",
					Data: ToolResultEvent{ToolCallID: tc.ID, Content: []llm.ContentBlock(toolContent), IsError: isError},
				})
			}
		}
	}

	return "", messages, fmt.Errorf("agent loop exceeded maximum iterations (%d)", MaxLoopIterations)
}

// formatToolResult converts a CallToolResult to []ContentBlock.
func formatToolResult(result *mcp.CallToolResult) llm.Content {
	if result == nil {
		return llm.Content{llm.NewTextContent("")}
	}
	var blocks []llm.ContentBlock
	for _, c := range result.Content {
		switch c.Type {
		case "image":
			blocks = append(blocks, llm.ContentBlock{
				Type:  "image",
				Image: &llm.ImageData{Data: c.Data, MimeType: c.MimeType},
			})
		default: // "text" or unknown
			blocks = append(blocks, llm.ContentBlock{
				Type: "text",
				Text: c.Text,
			})
		}
	}
	if len(blocks) == 0 {
		return llm.Content{llm.NewTextContent("")}
	}
	return llm.Content(blocks)
}

// truncateContentBlocks truncates text-type blocks to maxLen characters.
// Image blocks are passed through untouched.
func truncateContentBlocks(blocks llm.Content, maxLen int) llm.Content {
	result := make(llm.Content, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			b.Text = truncateOutput(b.Text, maxLen)
		}
		result = append(result, b)
	}
	return result
}

// truncateOutput truncates content to maxLen characters, appending a notice.
func truncateOutput(content string, maxLen int) string {
	if utf8.RuneCountInString(content) <= maxLen {
		return content
	}
	runes := []rune(content)
	truncated := string(runes[:maxLen])
	return truncated + fmt.Sprintf("\n\n[OUTPUT TRUNCATED: showing %d of %d characters]", maxLen, len(runes))
}
