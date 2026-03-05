package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/seaturt/server/internal/container"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/stdcopy"
)

// Transport manages the stdio connection to an MCP server running inside a container.
// It provides thread-safe request/response communication over a single docker exec session.
//
// Docker exec without TTY uses an 8-byte multiplexing header on each frame.
// Transport spins up a background goroutine that uses stdcopy.StdCopy to strip
// those headers, piping clean stdout into an io.Pipe the reader consumes from.
type Transport struct {
	containerID string
	command     string

	hijacked types.HijackedResponse
	reader   *bufio.Reader // reads from demuxed stdout pipe
	writer   io.Writer     // writes to hijacked.Conn (stdin)

	stdoutPipeW *io.PipeWriter // close to signal demux goroutine

	mu     sync.Mutex // protects writes
	closed bool
}

// TransportConfig holds the configuration for creating a Transport.
type TransportConfig struct {
	ContainerID string
	Command     string // MCP server binary name, e.g. "mcp-server-core"
}

// NewTransport creates a new stdio transport by running docker exec on the container.
func NewTransport(ctx context.Context, dockerMgr *container.Manager, cfg TransportConfig) (*Transport, error) {
	hijacked, err := dockerMgr.ExecStdio(ctx, cfg.ContainerID, container.ExecAttachOptions{
		Cmd: []string{cfg.Command},
	})
	if err != nil {
		return nil, fmt.Errorf("exec stdio for %s: %w", cfg.Command, err)
	}

	// Create a pipe for demuxed stdout
	stdoutR, stdoutW := io.Pipe()

	// Background goroutine: demux Docker multiplexed stream → clean stdout
	go func() {
		// stdcopy.StdCopy reads frames with 8-byte headers from hijacked.Reader
		// and writes raw payload to stdout / stderr writers.
		_, err := stdcopy.StdCopy(stdoutW, io.Discard, hijacked.Reader)
		if err != nil {
			stdoutW.CloseWithError(fmt.Errorf("stdcopy: %w", err))
		} else {
			stdoutW.Close()
		}
	}()

	t := &Transport{
		containerID: cfg.ContainerID,
		command:     cfg.Command,
		hijacked:    hijacked,
		reader:      bufio.NewReaderSize(stdoutR, 256*1024),
		writer:      hijacked.Conn,
		stdoutPipeW: stdoutW,
	}

	slog.Debug("transport created",
		"container_id", cfg.ContainerID[:12],
		"command", cfg.Command,
	)

	return t, nil
}

// Send sends a JSON-RPC request and returns the raw response bytes.
// This is a synchronous call — it writes the request and reads one response line.
func (t *Transport) Send(data []byte) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, fmt.Errorf("transport closed")
	}

	// Write request + newline
	if _, err := fmt.Fprintf(t.writer, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read response line — the demux goroutine has already stripped Docker
	// frame headers, so we get clean JSON lines here.
	line, err := t.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// SendNotification sends a JSON-RPC notification (no response expected).
func (t *Transport) SendNotification(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport closed")
	}

	if _, err := fmt.Fprintf(t.writer, "%s\n", data); err != nil {
		return fmt.Errorf("write notification: %w", err)
	}

	return nil
}

// Close terminates the docker exec session.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	slog.Debug("transport closing",
		"container_id", t.containerID[:12],
		"command", t.command,
	)

	t.hijacked.Close()
	return nil
}
