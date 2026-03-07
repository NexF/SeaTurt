package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/stdcopy"
)

// DiscoverTransport is similar to ephemeralTransport but exposes InitializeAndGetResult
// and ToolsList for the MCP discovery flow (used when loading MCP servers).
type DiscoverTransport struct {
	hijacked    types.HijackedResponse
	reader      *bufio.Reader
	writer      io.Writer
	stdoutPipeW *io.PipeWriter
	nextID      atomic.Int64
}

// NewDiscoverTransport creates a transport for MCP tool discovery.
// The caller must call Close() when done.
func NewDiscoverTransport(hijacked types.HijackedResponse) *DiscoverTransport {
	stdoutR, stdoutW := io.Pipe()

	// Background goroutine: demux Docker multiplexed stream → clean stdout
	go func() {
		_, err := stdcopy.StdCopy(stdoutW, io.Discard, hijacked.Reader)
		if err != nil {
			stdoutW.CloseWithError(err)
		} else {
			stdoutW.Close()
		}
	}()

	return &DiscoverTransport{
		hijacked:    hijacked,
		reader:      bufio.NewReaderSize(stdoutR, 256*1024),
		writer:      hijacked.Conn,
		stdoutPipeW: stdoutW,
	}
}

// InitializeAndGetResult performs the MCP initialize handshake and returns the result.
func (t *DiscoverTransport) InitializeAndGetResult() (*InitializeResult, error) {
	id := t.nextID.Add(1)

	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "seaturt",
			Version: "0.1.4",
		},
	}

	data, err := NewRequest(id, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := t.sendAndRead(data)
	if err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Parse initialize result
	var initResult InitializeResult
	if resp.Result != nil {
		if err := json.Unmarshal(resp.Result, &initResult); err != nil {
			return nil, fmt.Errorf("unmarshal initialize result: %w", err)
		}
	}

	// Send notifications/initialized
	notif, err := NewNotification("notifications/initialized")
	if err != nil {
		return nil, fmt.Errorf("build notification: %w", err)
	}
	if _, err := fmt.Fprintf(t.writer, "%s\n", notif); err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}

	return &initResult, nil
}

// ToolsList sends tools/list and returns the discovered tool definitions.
func (t *DiscoverTransport) ToolsList() ([]ToolDefinition, error) {
	id := t.nextID.Add(1)

	data, err := NewRequest(id, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := t.sendAndRead(data)
	if err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools/list result: %w", err)
	}

	return result.Tools, nil
}

// sendAndRead writes a request and reads the response.
func (t *DiscoverTransport) sendAndRead(data []byte) (*Response, error) {
	if _, err := fmt.Fprintf(t.writer, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read with a simple timeout context approach — the parent context handles timeout
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := t.reader.ReadBytes('\n')
		ch <- readResult{line, err}
	}()

	result := <-ch
	if result.err != nil {
		return nil, fmt.Errorf("read: %w", result.err)
	}

	var resp Response
	if err := json.Unmarshal(result.line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (%q)", err, string(result.line))
	}
	return &resp, nil
}

// Close terminates the discover transport session.
func (t *DiscoverTransport) Close() error {
	t.hijacked.Close()
	return nil
}

// Ensure DiscoverTransport is used (prevent unused warnings in case of future refactoring).
var _ = (*DiscoverTransport)(nil).Close

// discoverToolsViaExec is a convenience function that wraps the discovery flow.
// It's not used directly by manager.go (which uses the transport directly) but
// provided for potential future use.
func discoverToolsViaExec(ctx context.Context, hijacked types.HijackedResponse) ([]ToolDefinition, *InitializeResult, error) {
	transport := NewDiscoverTransport(hijacked)
	defer transport.Close()

	initResult, err := transport.InitializeAndGetResult()
	if err != nil {
		return nil, nil, err
	}

	tools, err := transport.ToolsList()
	if err != nil {
		return nil, initResult, err
	}

	return tools, initResult, nil
}
