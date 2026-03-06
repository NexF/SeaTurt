package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/seaturt/server/internal/container"
)

// Executor handles executing a single MCP tool call by starting a new
// docker exec process, performing the MCP handshake, calling the tool,
// and letting the process exit.
type Executor struct {
	dockerMgr   *container.Manager
	containerID string
	toolsDir    string // container path to .seaturt/tools/, used to locate bin files
}

// NewExecutor creates an Executor for executing MCP tool calls in a container.
func NewExecutor(dockerMgr *container.Manager, containerID string, toolsDir string) *Executor {
	return &Executor{
		dockerMgr:   dockerMgr,
		containerID: containerID,
		toolsDir:    toolsDir,
	}
}

// Execute runs a single tool call:
// 1. docker exec <container> <command>
// 2. MCP initialize handshake
// 3. tools/call with the given args
// 4. Read result + normalizeResult
// 5. Close stdin → process exits
//
// The context allows cancellation — closing the hijacked connection will
// cause the process to receive EOF on stdin and exit.
func (e *Executor) Execute(ctx context.Context, command string, toolName string, args map[string]any) (*CallToolResult, error) {
	// command is relative to toolsDir — build the full path
	cmdPath := filepath.Join(e.toolsDir, command)

	slog.Debug("executor starting tool call",
		"command", cmdPath,
		"tool", toolName,
		"container", e.containerID[:min(12, len(e.containerID))],
	)

	hijacked, err := e.dockerMgr.ExecStdio(ctx, e.containerID, container.ExecAttachOptions{
		Cmd: []string{cmdPath},
	})
	if err != nil {
		return nil, fmt.Errorf("exec stdio: %w", err)
	}
	defer hijacked.Close()

	// Build ephemeral transport for MCP communication
	transport := newEphemeralTransport(hijacked)
	defer transport.Close()

	// MCP initialize handshake
	if err := transport.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// tools/call
	result, err := transport.CallTool(ctx, toolName, args)
	if err != nil {
		return nil, fmt.Errorf("call tool: %w", err)
	}

	// Defensive normalization for non-standard MCP server responses
	return normalizeResult(result), nil
	// defer hijacked.Close() → stdin EOF → MCP Server process exits
}

// normalizeResult normalizes the MCP Server response to ensure consistent structure.
// Handles various non-standard cases to prevent agent loop crashes.
func normalizeResult(raw *CallToolResult) *CallToolResult {
	if raw != nil && len(raw.Content) > 0 {
		// Standard format — ensure each content item has a type field
		for i := range raw.Content {
			if raw.Content[i].Type == "" {
				raw.Content[i].Type = "text"
			}
		}
		return raw
	}

	// Fallback: no content or structural anomaly
	text := "(MCP Server returned empty response)"
	isError := false
	if raw != nil {
		isError = raw.IsError
		// If there are any other fields worth preserving, serialize them
		if raw.IsError {
			text = "(MCP Server returned error with no details)"
		}
	}
	return &CallToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
}
