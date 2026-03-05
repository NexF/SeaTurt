package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// --- JSON-RPC 2.0 Types ---

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP Protocol Types ---

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func main() {
	reader := bufio.NewReader(os.Stdin)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			return
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(nil, -32700, "parse error")
			continue
		}

		switch req.Method {
		case "initialize":
			handleInitialize(req)
		case "notifications/initialized":
			// client notification, no response needed
		case "tools/list":
			handleToolsList(req)
		case "tools/call":
			handleToolsCall(req)
		default:
			writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

func handleInitialize(req Request) {
	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
		ServerInfo: ServerInfo{
			Name:    "mcp-server-desktop",
			Version: "1.0.0",
		},
	}
	writeResult(req.ID, result)
}

func handleToolsList(req Request) {
	result := ToolsListResult{
		Tools: []ToolDefinition{
			{
				Name:        "screenshot",
				Description: "Take a screenshot of the desktop. Optionally specify a region.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":        "object",
							"description": "Optional region to capture {x, y, width, height}",
							"properties": map[string]any{
								"x":      map[string]any{"type": "integer"},
								"y":      map[string]any{"type": "integer"},
								"width":  map[string]any{"type": "integer"},
								"height": map[string]any{"type": "integer"},
							},
						},
					},
				},
			},
			{
				Name:        "mouse_click",
				Description: "Click the mouse at the specified coordinates",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{
							"type":        "integer",
							"description": "X coordinate",
						},
						"y": map[string]any{
							"type":        "integer",
							"description": "Y coordinate",
						},
						"button": map[string]any{
							"type":        "string",
							"description": "Mouse button: left, right, or middle",
							"enum":        []string{"left", "right", "middle"},
						},
					},
					"required": []string{"x", "y"},
				},
			},
			{
				Name:        "mouse_move",
				Description: "Move the mouse to the specified coordinates",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{
							"type":        "integer",
							"description": "X coordinate",
						},
						"y": map[string]any{
							"type":        "integer",
							"description": "Y coordinate",
						},
					},
					"required": []string{"x", "y"},
				},
			},
			{
				Name:        "mouse_drag",
				Description: "Drag the mouse from one position to another",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from_x": map[string]any{"type": "integer", "description": "Start X coordinate"},
						"from_y": map[string]any{"type": "integer", "description": "Start Y coordinate"},
						"to_x":   map[string]any{"type": "integer", "description": "End X coordinate"},
						"to_y":   map[string]any{"type": "integer", "description": "End Y coordinate"},
					},
					"required": []string{"from_x", "from_y", "to_x", "to_y"},
				},
			},
			{
				Name:        "keyboard_type",
				Description: "Type text using the keyboard",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "Text to type",
						},
					},
					"required": []string{"text"},
				},
			},
			{
				Name:        "keyboard_key",
				Description: "Press a key or key combination (e.g. 'Return', 'ctrl+c', 'alt+F4')",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key": map[string]any{
							"type":        "string",
							"description": "Key name or combination (e.g. 'Return', 'ctrl+c', 'alt+Tab')",
						},
					},
					"required": []string{"key"},
				},
			},
			{
				Name:        "window_list",
				Description: "List all visible windows on the desktop",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
			{
				Name:        "window_focus",
				Description: "Focus a window by its window ID or title substring",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"window_id": map[string]any{
							"type":        "string",
							"description": "X11 window ID (hex, e.g. '0x04000006')",
						},
						"title": map[string]any{
							"type":        "string",
							"description": "Window title substring to match",
						},
					},
				},
			},
			{
				Name:        "open_app",
				Description: "Open an application (e.g. 'firefox', 'gnome-terminal', 'nautilus')",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"app": map[string]any{
							"type":        "string",
							"description": "Application name or command to launch",
						},
					},
					"required": []string{"app"},
				},
			},
			{
				Name:        "desktop_wait",
				Description: "Wait for the desktop to render/stabilize, then take a screenshot",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"delay_ms": map[string]any{
							"type":        "integer",
							"description": "Milliseconds to wait before taking screenshot (default: 1000)",
						},
					},
				},
			},
		},
	}
	writeResult(req.ID, result)
}

func handleToolsCall(req Request) {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params")
		return
	}

	var result CallToolResult
	switch params.Name {
	case "screenshot":
		result = toolScreenshot(params.Arguments)
	case "mouse_click":
		result = toolMouseClick(params.Arguments)
	case "mouse_move":
		result = toolMouseMove(params.Arguments)
	case "mouse_drag":
		result = toolMouseDrag(params.Arguments)
	case "keyboard_type":
		result = toolKeyboardType(params.Arguments)
	case "keyboard_key":
		result = toolKeyboardKey(params.Arguments)
	case "window_list":
		result = toolWindowList(params.Arguments)
	case "window_focus":
		result = toolWindowFocus(params.Arguments)
	case "open_app":
		result = toolOpenApp(params.Arguments)
	case "desktop_wait":
		result = toolDesktopWait(params.Arguments)
	default:
		writeError(req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
		return
	}

	writeResult(req.ID, result)
}

func writeResult(id any, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(os.Stdout, "%s\n", data)
}

func writeError(id any, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(os.Stdout, "%s\n", data)
}
