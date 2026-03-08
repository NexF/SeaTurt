package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"
)

// ToolRouter is the interface for routing tool calls.
// mcp.Router implements this interface.
type ToolRouter interface {
	AllTools() []mcp.ToolDefinition
	Route(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error)
}

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
	Router       ToolRouter
	SystemPrompt string // if non-empty, use this prompt; otherwise fallback to DefaultSystemPrompt

	// OnMessage is called each time a new message is produced (assistant / tool).
	// It allows the caller to persist messages incrementally.
	OnMessage func(msg llm.ChatMessage)

	// OnToolCallStart is called when a tool call begins execution.
	// It provides the tool call ID and a cancel function for that specific tool call.
	OnToolCallStart func(toolCallID string, cancel context.CancelFunc)

	// OnToolCallEnd is called when a tool call finishes (success, error, or cancelled).
	OnToolCallEnd func(toolCallID string)
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

// ReasoningDelta is the data for a "reasoning_delta" event.
type ReasoningDelta struct {
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
func RunLoop(ctx context.Context, cfg LoopConfig, history []llm.ChatMessage, streamFn StreamFunc) (string, []llm.ChatMessage, error) {
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

	// Clear reasoning_content from old messages to save API bandwidth.
	// The API only requires reasoning_content from the same tool-call round.
	clearOldReasoningContent(messages)

	// On exit, backfill any assistant tool_calls that lack a corresponding tool result.
	// This happens when the loop is cancelled mid-execution (user interrupts a tool call).
	// Without this, the next LLM request would fail because the API requires every
	// tool_call to have a matching tool result message.
	defer func() {
		backfilled := backfillCancelledToolCalls(&messages)
		for _, msg := range backfilled {
			if cfg.OnMessage != nil {
				cfg.OnMessage(msg)
			}
			if streamFn != nil {
				streamFn(StreamEvent{
					Type: "tool_result",
					Data: ToolResultEvent{
						ToolCallID: msg.ToolCallID,
						Content:    []llm.ContentBlock(msg.Content),
						IsError:    true,
					},
				})
			}
		}
	}()

	var finalContent string

	for i := 0; i < MaxLoopIterations; i++ {
		// Checkpoint: check ctx at the start of each iteration
		if err := ctx.Err(); err != nil {
			if streamFn != nil {
				streamFn(StreamEvent{Type: "cancelled", Data: nil})
			}
			return "", messages, fmt.Errorf("cancelled: %w", err)
		}

		slog.Debug("agent loop iteration", "i", i)

		var resp *llm.ChatResponse
		var err error

		if streamFn != nil {
			resp, err = cfg.LLMClient.ChatCompletionStream(ctx, messages, toolDefs, func(delta llm.StreamDelta) error {
				for _, choice := range delta.Choices {
					if choice.Delta != nil {
						if text := choice.Delta.Content.String(); text != "" {
							streamFn(StreamEvent{
								Type: "text_delta",
								Data: TextDelta{Content: text},
							})
						}
						if choice.Delta.ReasoningContent != "" {
							streamFn(StreamEvent{
								Type: "reasoning_delta",
								Data: ReasoningDelta{Content: choice.Delta.ReasoningContent},
							})
						}
					}
				}
				return nil
			})
		} else {
			resp, err = cfg.LLMClient.ChatCompletion(ctx, messages, toolDefs)
		}

		if err != nil {
			slog.Error("LLM call failed",
				"iteration", i,
				"messages", len(messages),
				"tools", len(toolDefs),
				"error", err,
			)

			// If there's a partial response (e.g. stream interrupted by cancellation),
			// persist the accumulated assistant message so it's not lost.
			if resp != nil {
				slog.Info("partial response check",
					"choices_len", len(resp.Choices),
					"resp_id", resp.ID,
				)
				if len(resp.Choices) > 0 {
					partialMsg := resp.Choices[0].Message
					slog.Info("partial message details",
						"role", partialMsg.Role,
						"content_str", partialMsg.Content.String(),
						"content_len", len(partialMsg.Content.String()),
						"content_blocks", len(partialMsg.Content),
						"tool_calls", len(partialMsg.ToolCalls),
					)
					if partialMsg.Content.String() != "" || len(partialMsg.ToolCalls) > 0 {
						messages = append(messages, partialMsg)
						if cfg.OnMessage != nil {
							cfg.OnMessage(partialMsg)
						}
						slog.Info("saved partial assistant message on interruption",
							"content_len", len(partialMsg.Content.String()),
							"tool_calls", len(partialMsg.ToolCalls),
						)
					}
				}
			} else {
				slog.Info("no partial response (resp is nil)")
			}

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
		if cfg.OnMessage != nil {
			cfg.OnMessage(assistantMsg)
		}

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
			// Checkpoint: check ctx before each tool call
			if err := ctx.Err(); err != nil {
				if streamFn != nil {
					streamFn(StreamEvent{Type: "cancelled", Data: nil})
				}
				return "", messages, fmt.Errorf("cancelled: %w", err)
			}

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
					// Try to salvage: if LLM concatenated multiple JSON objects (e.g. {...}{...}),
					// extract the first valid one and warn about the rest.
					salvaged := false
					raw := strings.TrimSpace(tc.Function.Arguments)
					if dec := json.NewDecoder(strings.NewReader(raw)); dec.More() {
						var first map[string]any
						if decErr := dec.Decode(&first); decErr == nil && dec.InputOffset() < int64(len(raw)) {
							args = first
							salvaged = true
							slog.Warn("tool call contained concatenated JSON objects, using first one",
								"tool", tc.Function.Name,
								"raw_length", len(raw),
								"used_offset", dec.InputOffset(),
							)
						}
					}

					if !salvaged {
						toolResult := fmt.Sprintf(
							"Error: arguments is not valid JSON — %s. "+
								"Each tool call must have exactly one JSON object as arguments. "+
								"If you need to call multiple tools, make separate tool_use calls.",
							err.Error(),
						)
						toolResultBlocks := []llm.ContentBlock{llm.NewTextContent(toolResult)}
						toolMsg := llm.ChatMessage{
							Role:       "tool",
							Content:    llm.Content(toolResultBlocks),
							ToolCallID: tc.ID,
						}
						messages = append(messages, toolMsg)
						if cfg.OnMessage != nil {
							cfg.OnMessage(toolMsg)
						}
						if streamFn != nil {
							streamFn(StreamEvent{
								Type: "tool_result",
								Data: ToolResultEvent{ToolCallID: tc.ID, Content: toolResultBlocks, IsError: true},
							})
						}
						continue
					}
				}
			}

			// Create per-tool-call context (derived from chat ctx)
			toolCtx, toolCancel := context.WithCancel(ctx)

			// Register tool call cancel function for external cancellation
			if cfg.OnToolCallStart != nil {
				cfg.OnToolCallStart(tc.ID, toolCancel)
			}

			// Execute via router (pass toolCtx for per-tool cancellation)
			result, err := cfg.Router.Route(toolCtx, tc.Function.Name, args)

			// Check cancellation BEFORE calling toolCancel() — otherwise
			// toolCtx.Err() would always be non-nil and we can't distinguish
			// user-cancelled from normal completion.
			wasCancelled := err != nil && toolCtx.Err() != nil && ctx.Err() == nil

			// Clean up: cancel context to prevent leak + unregister
			toolCancel()
			if cfg.OnToolCallEnd != nil {
				cfg.OnToolCallEnd(tc.ID)
			}

			// Check if this was a per-tool cancellation (toolCtx cancelled but chat ctx still alive)
			if wasCancelled {
				// Single tool call cancelled by user, but chat continues
				cancelledContent := llm.Content{llm.NewTextContent("用户取消了此工具调用")}
				toolMsg := llm.ChatMessage{
					Role:       "tool",
					Content:    cancelledContent,
					ToolCallID: tc.ID,
				}
				messages = append(messages, toolMsg)
				if cfg.OnMessage != nil {
					cfg.OnMessage(toolMsg)
				}
				if streamFn != nil {
					streamFn(StreamEvent{
						Type: "tool_result",
						Data: ToolResultEvent{
							ToolCallID: tc.ID,
							Content:    []llm.ContentBlock(cancelledContent),
							IsError:    true,
						},
					})
				}
				// Don't return — continue processing remaining tool calls or next LLM round
				continue
			}

			// Check if chat-level context was cancelled
			if err != nil && ctx.Err() != nil {
				if streamFn != nil {
					streamFn(StreamEvent{Type: "cancelled", Data: nil})
				}
				return "", messages, fmt.Errorf("cancelled: %w", ctx.Err())
			}

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

			toolMsg := llm.ChatMessage{
				Role:       "tool",
				Content:    toolContent,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolMsg)
			if cfg.OnMessage != nil {
				cfg.OnMessage(toolMsg)
			}

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

// backfillCancelledToolCalls scans the message history from the end and injects
// synthetic "cancelled" tool result messages for any assistant tool_calls that
// don't have a corresponding tool result. This repairs the message history so
// the next LLM API call won't fail with a parameter error.
//
// It modifies the messages slice in-place (via the pointer) and returns the
// list of newly injected messages (so the caller can persist/stream them).
func backfillCancelledToolCalls(messages *[]llm.ChatMessage) []llm.ChatMessage {
	msgs := *messages
	if len(msgs) == 0 {
		return nil
	}

	// Walk backwards to find the last assistant message with tool_calls.
	// Collect tool_call IDs that already have a matching tool result after it.
	var lastAssistantIdx int = -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
			lastAssistantIdx = i
			break
		}
	}
	if lastAssistantIdx < 0 {
		return nil
	}

	// Collect which tool_call IDs already have results
	answeredIDs := make(map[string]bool)
	for i := lastAssistantIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role == "tool" && msgs[i].ToolCallID != "" {
			answeredIDs[msgs[i].ToolCallID] = true
		}
	}

	// Find missing ones
	var injected []llm.ChatMessage
	for _, tc := range msgs[lastAssistantIdx].ToolCalls {
		if answeredIDs[tc.ID] {
			continue
		}
		cancelMsg := llm.ChatMessage{
			Role:       "tool",
			Content:    llm.Content{llm.NewTextContent("工具调用被取消（用户中断）")},
			ToolCallID: tc.ID,
		}
		injected = append(injected, cancelMsg)
		slog.Info("backfilled cancelled tool result",
			"tool_call_id", tc.ID,
			"tool_name", tc.Function.Name,
		)
	}

	if len(injected) > 0 {
		*messages = append(msgs, injected...)
	}
	return injected
}

// clearOldReasoningContent removes reasoning_content from all messages except
// those in the last assistant+tool round. The DeepSeek API requires
// reasoning_content to be preserved within the same tool-call round, but
// sending it for historical messages wastes tokens and bandwidth.
func clearOldReasoningContent(messages []llm.ChatMessage) {
	// Find the start of the last assistant+tool round
	lastAssistantIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			lastAssistantIdx = i
			// Continue backward to find the first assistant in this contiguous round
			// (there may be multiple assistant+tool pairs in one round)
			for j := i - 1; j >= 0; j-- {
				if messages[j].Role == "tool" || messages[j].Role == "assistant" {
					if messages[j].Role == "assistant" {
						lastAssistantIdx = j
					}
				} else {
					break
				}
			}
			break
		}
	}

	// Clear reasoning_content from all messages before the last round
	for i := 0; i < lastAssistantIdx; i++ {
		messages[i].ReasoningContent = ""
	}
}
