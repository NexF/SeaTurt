package builtin

import (
	"context"
	"fmt"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/mcp"
)

// Router implements agent.ToolRouter for built-in tools.
// Tool names are in "builtin-toolname" format (e.g., "builtin-create_cron_job").
type Router struct {
	tools    []mcp.ToolDefinition
	handlers map[string]Handler // toolName (without prefix) -> handler
}

// NewRouter creates a BuiltinRouter with the given handlers.
func NewRouter(handlers map[string]Handler) *Router {
	// Build qualified tool definitions (with "builtin-" prefix)
	rawDefs := CronToolDefinitions()
	qualifiedDefs := make([]mcp.ToolDefinition, 0, len(rawDefs))
	for _, d := range rawDefs {
		qualifiedDefs = append(qualifiedDefs, mcp.ToolDefinition{
			Name:        builtinServerName + "-" + d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}

	return &Router{
		tools:    qualifiedDefs,
		handlers: handlers,
	}
}

// AllTools returns all built-in tool definitions with "builtin-" prefix.
func (r *Router) AllTools() []mcp.ToolDefinition {
	return r.tools
}

// Route executes a built-in tool call.
// qualifiedName is in "builtin-toolname" format.
func (r *Router) Route(ctx context.Context, qualifiedName string, args map[string]any) (*mcp.CallToolResult, error) {
	_, toolName, err := mcp.SplitToolName(qualifiedName)
	if err != nil {
		return nil, err
	}

	handler, ok := r.handlers[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown builtin tool: %s", toolName)
	}

	// Extract agentID from context (set by chat handler / ExecutePrompt)
	agentID, _ := ctx.Value(agent.AgentIDContextKey).(string)
	if agentID == "" {
		return nil, fmt.Errorf("agentID not found in context")
	}

	return handler.Handle(ctx, agentID, args)
}
