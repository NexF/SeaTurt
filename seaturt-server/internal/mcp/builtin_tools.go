package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BuiltinServers holds the predefined tool definitions for built-in MCP servers.
// These are written to workspace/.seaturt/tools/ when an agent is created.
var BuiltinServers = map[string]ServerDef{
	"core": {
		Name:        "core",
		Command:     "mcp-server-core",
		Description: "基础 shell/file 操作",
		Enabled:     true,
		Tools: []ToolDefinition{
			{
				Name:        "shell_exec",
				Description: "Execute a shell command and return stdout/stderr. Default timeout is 120s. Use background=true for long-running processes (servers, browsers, etc.) — returns immediately with PID.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "The shell command to execute",
						},
						"timeout": map[string]any{
							"type":        "number",
							"description": "Timeout in seconds (default: 120, max: 1800). Command is killed if exceeded.",
						},
						"background": map[string]any{
							"type":        "boolean",
							"description": "If true, start the command in background and return immediately with PID. Use for long-running processes like servers, browsers, etc.",
						},
					},
					"required": []string{"command"},
				},
			},
			{
				Name:        "file_read",
				Description: "Read the contents of a file",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path to the file to read",
						},
					},
					"required": []string{"path"},
				},
			},
			{
				Name:        "file_write",
				Description: "Write content to a file (creates or overwrites)",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path to the file to write",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Content to write to the file",
						},
					},
					"required": []string{"path", "content"},
				},
			},
			{
				Name:        "file_list",
				Description: "List files and directories at the given path",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Directory path to list",
						},
					},
					"required": []string{"path"},
				},
			},
		},
	},
	"desktop": {
		Name:        "desktop",
		Command:     "mcp-server-desktop",
		Description: "桌面 GUI 操作",
		Enabled:     true,
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
	},
}

// WriteBuiltinTools writes the built-in tool definition YAML files to the given directory.
// It creates the directory if it doesn't exist.
// If serverNames is nil, all built-in servers are written.
func WriteBuiltinTools(dir string, serverNames []string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}

	if serverNames == nil {
		serverNames = make([]string, 0, len(BuiltinServers))
		for name := range BuiltinServers {
			serverNames = append(serverNames, name)
		}
	}

	for _, name := range serverNames {
		srv, ok := BuiltinServers[name]
		if !ok {
			return fmt.Errorf("unknown builtin server: %s", name)
		}

		// Convert to yamlServerDef for proper YAML serialization
		ysd := yamlServerDef{
			Name:        srv.Name,
			Command:     srv.Command,
			Description: srv.Description,
			Enabled:     srv.Enabled,
			Tools:       make([]yamlToolDef, 0, len(srv.Tools)),
		}
		for _, t := range srv.Tools {
			schema, _ := toMapStringAny(t.InputSchema)
			ysd.Tools = append(ysd.Tools, yamlToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
		}

		data, err := yaml.Marshal(ysd)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", name, err)
		}

		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	return nil
}

// toMapStringAny converts an any value to map[string]any.
// If the input is already map[string]any, returns it directly.
// Otherwise returns nil.
func toMapStringAny(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	return nil, false
}
