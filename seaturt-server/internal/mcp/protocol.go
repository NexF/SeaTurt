package mcp

import "encoding/json"

// --- JSON-RPC 2.0 ---

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return e.Message
}

// --- MCP Protocol Types ---

// InitializeParams is sent by the client to begin the MCP handshake.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

// ClientInfo identifies this MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the server's response to initialize.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
}

// ServerInfo identifies the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ToolDefinition describes a single tool exposed by an MCP server.
type ToolDefinition struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	InputSchema any    `json:"inputSchema" yaml:"inputSchema"`
}

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// CallToolParams is the request body for tools/call.
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolContent is a single content block in a tool result.
type ToolContent struct {
	Type     string `json:"type"`              // "text" | "image" | "resource"
	Text     string `json:"text,omitempty"`    // type=text
	Data     string `json:"data,omitempty"`    // type=image, base64 encoded
	MimeType string `json:"mimeType,omitempty"` // type=image, e.g. "image/png"
}

// CallToolResult is the response to tools/call.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// NewRequest builds a JSON-RPC 2.0 request with the given method and params.
func NewRequest(id int64, method string, params any) ([]byte, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = b
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}
	return json.Marshal(req)
}

// NewNotification builds a JSON-RPC 2.0 notification (no id).
func NewNotification(method string) ([]byte, error) {
	req := Request{
		JSONRPC: "2.0",
		Method:  method,
	}
	return json.Marshal(req)
}
