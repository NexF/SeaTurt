package llm

import (
	"github.com/seaturt/server/internal/mcp"
)

// ConvertMCPTools converts MCP ToolDefinitions to OpenAI-compatible ToolDefs.
func ConvertMCPTools(mcpTools []mcp.ToolDefinition) []ToolDef {
	defs := make([]ToolDef, 0, len(mcpTools))
	for _, t := range mcpTools {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  normalizeSchema(t.InputSchema),
			},
		})
	}
	return defs
}

// normalizeSchema ensures the input schema is a valid JSON Schema object.
// MCP servers may provide the schema as a map or nil.
func normalizeSchema(schema any) any {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return schema
}
