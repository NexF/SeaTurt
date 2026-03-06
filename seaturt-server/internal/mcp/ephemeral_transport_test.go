package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestEphemeralTransport creates an ephemeralTransport backed by in-memory net.Pipe.
// Returns the transport, a server-side conn for reading requests, and a pipe writer
// for sending responses. The caller must close all resources.
func newTestEphemeralTransport() (*ephemeralTransport, net.Conn, *io.PipeWriter, func()) {
	clientConn, serverConn := net.Pipe()
	responseRead, responseWrite := io.Pipe()

	t := &ephemeralTransport{
		reader: bufio.NewReaderSize(responseRead, 256*1024),
		writer: clientConn,
		hijacked: types.HijackedResponse{
			Conn:   clientConn,
			Reader: bufio.NewReader(clientConn),
		},
	}

	cleanup := func() {
		clientConn.Close()
		serverConn.Close()
		responseRead.Close()
		responseWrite.Close()
	}

	return t, serverConn, responseWrite, cleanup
}

// --- ephemeralTransport.Initialize tests ---

func TestEphemeralTransport_Initialize(t *testing.T) {
	t.Parallel()

	transport, serverConn, responseWrite, cleanup := newTestEphemeralTransport()
	defer cleanup()

	// Server goroutine: read initialize request, send response
	go func() {
		reader := bufio.NewReader(serverConn)

		// Read initialize request
		line, _ := reader.ReadBytes('\n')
		var req Request
		json.Unmarshal(line, &req)

		if req.Method != "initialize" {
			t.Errorf("expected initialize, got %s", req.Method)
			return
		}

		// Send initialize response
		initResult := InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      ServerInfo{Name: "test-server", Version: "1.0.0"},
		}
		resultData, _ := json.Marshal(initResult)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(resultData),
		}
		respData, _ := json.Marshal(resp)
		responseWrite.Write(append(respData, '\n'))

		// Read notifications/initialized (consume it, no response)
		reader.ReadBytes('\n')
	}()

	err := transport.Initialize()
	require.NoError(t, err)
}

func TestEphemeralTransport_Initialize_Error(t *testing.T) {
	t.Parallel()

	transport, serverConn, responseWrite, cleanup := newTestEphemeralTransport()
	defer cleanup()

	// Server: respond with error
	go func() {
		reader := bufio.NewReader(serverConn)
		line, _ := reader.ReadBytes('\n')
		var req Request
		json.Unmarshal(line, &req)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32600, Message: "bad request"},
		}
		respData, _ := json.Marshal(resp)
		responseWrite.Write(append(respData, '\n'))
	}()

	err := transport.Initialize()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
}

// --- ephemeralTransport.CallTool tests ---

func TestEphemeralTransport_CallTool(t *testing.T) {
	t.Parallel()

	transport, serverConn, responseWrite, cleanup := newTestEphemeralTransport()
	defer cleanup()

	// Server: read request, verify tool name, send response
	go func() {
		reader := bufio.NewReader(serverConn)
		line, _ := reader.ReadBytes('\n')
		var req Request
		json.Unmarshal(line, &req)

		if req.Method != "tools/call" {
			t.Errorf("expected tools/call, got %s", req.Method)
			return
		}

		// Verify tool name is the original (not prefixed)
		var params CallToolParams
		json.Unmarshal(req.Params, &params)
		if params.Name != "shell_exec" {
			t.Errorf("expected shell_exec, got %s", params.Name)
		}

		toolResult := CallToolResult{
			Content: []ToolContent{{Type: "text", Text: "hello world"}},
			IsError: false,
		}
		resultData, _ := json.Marshal(toolResult)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(resultData),
		}
		respData, _ := json.Marshal(resp)
		responseWrite.Write(append(respData, '\n'))
	}()

	result, err := transport.CallTool(context.Background(), "shell_exec", map[string]any{
		"command": "echo hello",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Equal(t, "hello world", result.Content[0].Text)
	assert.False(t, result.IsError)
}

func TestEphemeralTransport_CallTool_ContextCancel(t *testing.T) {
	t.Parallel()

	transport, serverConn, _, cleanup := newTestEphemeralTransport()
	defer cleanup()

	// Drain server side to prevent write blocking
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := serverConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := transport.CallTool(ctx, "shell_exec", map[string]any{"command": "sleep 100"})
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancelled")
}

func TestEphemeralTransport_CallTool_RPCError(t *testing.T) {
	t.Parallel()

	transport, serverConn, responseWrite, cleanup := newTestEphemeralTransport()
	defer cleanup()

	go func() {
		reader := bufio.NewReader(serverConn)
		line, _ := reader.ReadBytes('\n')
		var req Request
		json.Unmarshal(line, &req)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "unknown tool: foobar"},
		}
		respData, _ := json.Marshal(resp)
		responseWrite.Write(append(respData, '\n'))
	}()

	result, err := transport.CallTool(context.Background(), "foobar", nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: foobar")
}

// --- normalizeResult tests ---

func TestNormalizeResult_Standard(t *testing.T) {
	input := &CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "hello"},
		},
		IsError: false,
	}
	result := normalizeResult(input)
	assert.Equal(t, input, result)
}

func TestNormalizeResult_MissingType(t *testing.T) {
	input := &CallToolResult{
		Content: []ToolContent{
			{Text: "hello"}, // missing type
		},
	}
	result := normalizeResult(input)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Equal(t, "hello", result.Content[0].Text)
}

func TestNormalizeResult_EmptyContent(t *testing.T) {
	input := &CallToolResult{
		Content: []ToolContent{},
	}
	result := normalizeResult(input)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Contains(t, result.Content[0].Text, "MCP Server returned empty response")
}

func TestNormalizeResult_Nil(t *testing.T) {
	result := normalizeResult(nil)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Contains(t, result.Content[0].Text, "MCP Server returned empty response")
}

func TestNormalizeResult_NoContentField(t *testing.T) {
	// Simulates a non-standard response that has IsError but no content
	input := &CallToolResult{
		IsError: true,
	}
	result := normalizeResult(input)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Contains(t, result.Content[0].Text, "error with no details")
	assert.True(t, result.IsError) // IsError should be preserved
}

func TestNormalizeResult_ImageContent(t *testing.T) {
	input := &CallToolResult{
		Content: []ToolContent{
			{Type: "image", Data: "base64data", MimeType: "image/png"},
		},
	}
	result := normalizeResult(input)
	assert.Equal(t, input, result)
	assert.Equal(t, "image", result.Content[0].Type)
}

func TestNormalizeResult_MixedContent(t *testing.T) {
	input := &CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "output"},
			{Data: "base64", MimeType: "image/png"}, // missing type
		},
	}
	result := normalizeResult(input)
	assert.Len(t, result.Content, 2)
	assert.Equal(t, "text", result.Content[0].Type)
	assert.Equal(t, "text", result.Content[1].Type) // defaulted to "text"
}

// --- Full flow test: Initialize + CallTool ---

func TestEphemeralTransport_FullFlow(t *testing.T) {
	t.Parallel()

	transport, serverConn, responseWrite, cleanup := newTestEphemeralTransport()
	defer cleanup()

	go func() {
		reader := bufio.NewReader(serverConn)

		// 1. Read initialize request
		line, _ := reader.ReadBytes('\n')
		var initReq Request
		json.Unmarshal(line, &initReq)

		// Send initialize response
		initResult := InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      ServerInfo{Name: "test-server", Version: "1.0.0"},
		}
		resultData, _ := json.Marshal(initResult)
		resp := Response{JSONRPC: "2.0", ID: initReq.ID, Result: json.RawMessage(resultData)}
		respData, _ := json.Marshal(resp)
		responseWrite.Write(append(respData, '\n'))

		// 2. Read notifications/initialized
		reader.ReadBytes('\n')

		// 3. Read tools/call request
		line, _ = reader.ReadBytes('\n')
		var callReq Request
		json.Unmarshal(line, &callReq)

		// Send tools/call response
		callResult := CallToolResult{
			Content: []ToolContent{{Type: "text", Text: "executed: ls"}},
			IsError: false,
		}
		callResultData, _ := json.Marshal(callResult)
		callResp := Response{JSONRPC: "2.0", ID: callReq.ID, Result: json.RawMessage(callResultData)}
		callRespData, _ := json.Marshal(callResp)
		responseWrite.Write(append(callRespData, '\n'))
	}()

	// Initialize
	err := transport.Initialize()
	require.NoError(t, err)

	// CallTool
	result, err := transport.CallTool(context.Background(), "shell_exec", map[string]any{
		"command": "ls",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Content, 1)
	assert.Equal(t, "executed: ls", result.Content[0].Text)
}
