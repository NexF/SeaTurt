package mcp

import (
	"fmt"
	"log/slog"
	"sync"
)

// Router maps tool names to their corresponding MCP clients.
// When the LLM issues a tool_call, the Router determines which MCP server handles it.
type Router struct {
	mu    sync.RWMutex
	routes map[string]*Client // tool_name -> client
}

// NewRouter creates a Router from an MCP client Pool.
// It builds the routing table by iterating all clients and their tools.
func NewRouter(pool *Pool) *Router {
	r := &Router{
		routes: make(map[string]*Client),
	}
	r.Rebuild(pool)
	return r
}

// Rebuild reconstructs the routing table from the current pool state.
// Call this after adding/removing MCP servers from the pool.
func (r *Router) Rebuild(pool *Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.routes = make(map[string]*Client)
	for _, client := range pool.AllClients() {
		for _, tool := range client.Tools() {
			if existing, ok := r.routes[tool.Name]; ok {
				slog.Warn("duplicate tool name, later server wins",
					"tool", tool.Name,
					"existing_server", existing.Name(),
					"new_server", client.Name(),
				)
			}
			r.routes[tool.Name] = client
		}
	}

	slog.Info("tool router rebuilt", "routes", len(r.routes))
}

// Route executes a tool call by looking up the appropriate MCP client and forwarding the request.
func (r *Router) Route(toolName string, args map[string]any) (*CallToolResult, error) {
	r.mu.RLock()
	client, ok := r.routes[toolName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	slog.Debug("routing tool call",
		"tool", toolName,
		"server", client.Name(),
	)

	result, err := client.CallTool(toolName, args)
	if err != nil {
		return nil, fmt.Errorf("call tool %s on %s: %w", toolName, client.Name(), err)
	}

	return result, nil
}

// ToolNames returns all registered tool names.
func (r *Router) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.routes))
	for name := range r.routes {
		names = append(names, name)
	}
	return names
}

// AllTools returns all tool definitions from the routing table.
func (r *Router) AllTools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var tools []ToolDefinition
	for _, client := range r.routes {
		for _, tool := range client.Tools() {
			if !seen[tool.Name] {
				seen[tool.Name] = true
				tools = append(tools, tool)
			}
		}
	}
	return tools
}
