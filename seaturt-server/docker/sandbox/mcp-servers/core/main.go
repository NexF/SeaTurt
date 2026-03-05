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
	Data     string `json:"data,omitempty"`     // type=image, base64 encoded
	MimeType string `json:"mimeType,omitempty"` // type=image, e.g. "image/png"
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
			Name:    "mcp-server-core",
			Version: "1.0.0",
		},
	}
	writeResult(req.ID, result)
}

func handleToolsList(req Request) {
	result := ToolsListResult{
		Tools: []ToolDefinition{
			{
				Name:        "shell_exec",
				Description: "Execute a shell command and return stdout/stderr",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "The shell command to execute",
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
	case "shell_exec":
		result = toolShellExec(params.Arguments)
	case "file_read":
		result = toolFileRead(params.Arguments)
	case "file_write":
		result = toolFileWrite(params.Arguments)
	case "file_list":
		result = toolFileList(params.Arguments)
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
