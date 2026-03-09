package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/seaturt/server/internal/mcp"
)

// Handler is the interface for a single built-in tool handler.
type Handler interface {
	Handle(ctx context.Context, agentID string, args map[string]any) (*mcp.CallToolResult, error)
}

// textResult returns a successful text result.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
	}
}

// errorResult returns an error result.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// jsonResult returns a successful JSON result.
func jsonResult(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("failed to marshal result: %v", err))
	}
	return textResult(string(data))
}

// getString extracts a string value from args.
func getString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// getBool extracts a bool value from args (handles JSON number too).
func getBool(args map[string]any, key string) (bool, bool) {
	v, ok := args[key]
	if !ok {
		return false, false
	}
	switch val := v.(type) {
	case bool:
		return val, true
	case float64:
		return val != 0, true
	default:
		return false, false
	}
}
