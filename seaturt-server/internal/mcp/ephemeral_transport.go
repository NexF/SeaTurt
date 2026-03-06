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

// ephemeralTransport wraps a single docker exec session for one MCP interaction.
// It handles the Docker stdout demuxing and JSON-RPC communication.
//
// Unlike the long-lived Transport, this is designed for a single use:
// initialize → tools/call → close. No mutex needed (single caller, no concurrency).
type ephemeralTransport struct {
	hijacked    types.HijackedResponse
	reader      *bufio.Reader
	writer      io.Writer
	stdoutPipeW *io.PipeWriter
	nextID      atomic.Int64
}

// newEphemeralTransport creates a one-shot transport from a docker exec hijacked response.
// The caller must call Close() when done.
func newEphemeralTransport(hijacked types.HijackedResponse) *ephemeralTransport {
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

	return &ephemeralTransport{
		hijacked:    hijacked,
		reader:      bufio.NewReaderSize(stdoutR, 256*1024),
		writer:      hijacked.Conn,
		stdoutPipeW: stdoutW,
	}
}

// Initialize performs the MCP JSON-RPC initialize handshake.
func (t *ephemeralTransport) Initialize() error {
	id := t.nextID.Add(1)

	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "seaturt",
			Version: "1.0.0",
		},
	}

	data, err := NewRequest(id, "initialize", params)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := t.sendAndRead(context.Background(), data)
	if err != nil {
		return fmt.Errorf("send initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Send notifications/initialized
	notif, err := NewNotification("notifications/initialized")
	if err != nil {
		return fmt.Errorf("build notification: %w", err)
	}
	if _, err := fmt.Fprintf(t.writer, "%s\n", notif); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}

	return nil
}

// CallTool sends tools/call and reads the response. Supports context cancellation.
// When ctx is cancelled, the hijacked connection is closed, killing the MCP server process.
func (t *ephemeralTransport) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	id := t.nextID.Add(1)

	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	data, err := NewRequest(id, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := t.sendAndRead(ctx, data)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call error: %s", resp.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return &result, nil
}

// sendAndRead writes a request and reads the response, with context cancellation support.
func (t *ephemeralTransport) sendAndRead(ctx context.Context, data []byte) (*Response, error) {
	// Write request + newline
	if _, err := fmt.Fprintf(t.writer, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read response with context cancellation support
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := t.reader.ReadBytes('\n')
		ch <- readResult{line, err}
	}()

	select {
	case <-ctx.Done():
		// Cancel: close the hijacked connection, which kills the MCP server process.
		// No need to drain — the process is dead.
		t.hijacked.Close()
		return nil, fmt.Errorf("cancelled: %w", ctx.Err())
	case result := <-ch:
		if result.err != nil {
			return nil, fmt.Errorf("read: %w", result.err)
		}
		var resp Response
		if err := json.Unmarshal(result.line, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		return &resp, nil
	}
}

// Close terminates the ephemeral transport session.
func (t *ephemeralTransport) Close() error {
	t.hijacked.Close()
	return nil
}
