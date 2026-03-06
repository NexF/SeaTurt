package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Router maps tool names (in "mcpname-toolname" format) to their MCP servers
// and executes tool calls via the Executor.
type Router struct {
	mu       sync.RWMutex
	registry *ToolRegistry
	executor *Executor
}

// NewRouter creates a Router from a ToolRegistry and Executor.
func NewRouter(registry *ToolRegistry, executor *Executor) *Router {
	return &Router{
		registry: registry,
		executor: executor,
	}
}

// Route executes a tool call:
// 1. Parse "mcpname-toolname" → serverName + originalToolName
// 2. Look up server definition from registry
// 3. Execute via Executor with the original tool name
func (r *Router) Route(ctx context.Context, qualifiedName string, args map[string]any) (*CallToolResult, error) {
	serverName, toolName, err := SplitToolName(qualifiedName)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	server, err := r.registry.GetServer(serverName)
	r.mu.RUnlock()
	if err != nil {
		return nil, err
	}

	slog.Debug("routing tool call",
		"qualified_name", qualifiedName,
		"server", serverName,
		"tool", toolName,
		"command", server.Command,
	)

	// Execute with the original tool name (MCP Server doesn't know about the prefix)
	result, err := r.executor.Execute(ctx, server.Command, toolName, args)
	if err != nil {
		return nil, fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
	}

	return result, nil
}

// AllTools returns all tool definitions from the registry.
// Tool names are in "mcpname-toolname" format (e.g. "core-shell_exec").
func (r *Router) AllTools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.registry.AllTools()
}

// ToolNames returns all registered tool names.
func (r *Router) ToolNames() []string {
	tools := r.AllTools()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}
