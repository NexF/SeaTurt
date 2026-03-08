package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const socketPath = "/tmp/mcp-browser.sock"
const socketTimeout = 60 * time.Second
const connectRetries = 60                        // max retries waiting for daemon
const connectRetryInterval = 500 * time.Millisecond // 500ms between retries, total ~30s max

// JSON-RPC 2.0 types (same as core/desktop MCP servers)

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

		// Notifications don't need forwarding (just consume them)
		if req.Method == "notifications/initialized" {
			continue
		}

		// Forward request to daemon via Unix socket and relay response
		resp, err := forwardToDaemon(line)
		if err != nil {
			writeError(req.ID, -32603, fmt.Sprintf("daemon communication error: %v", err))
			continue
		}

		// Write raw response to stdout.
		// resp from ReadBytes('\n') already contains trailing newline,
		// so use %s (not %s\n) to avoid emitting a double newline.
		os.Stdout.Write(resp)
	}
}

// forwardToDaemon sends a JSON-RPC request to the daemon via Unix socket
// and reads back the response. Each call opens a new connection.
// It retries connection if the daemon socket is not yet available.
func forwardToDaemon(reqData []byte) ([]byte, error) {
	var conn net.Conn
	var err error
	for i := 0; i < connectRetries; i++ {
		conn, err = net.DialTimeout("unix", socketPath, 5*time.Second)
		if err == nil {
			break
		}
		if i < connectRetries-1 {
			fmt.Fprintf(os.Stderr, "waiting for daemon socket (%d/%d): %v\n", i+1, connectRetries, err)
			time.Sleep(connectRetryInterval)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connect to daemon after %d retries: %w", connectRetries, err)
	}
	defer conn.Close()

	// Set read/write deadline
	_ = conn.SetDeadline(time.Now().Add(socketTimeout))

	// Send request
	if _, err := conn.Write(reqData); err != nil {
		return nil, fmt.Errorf("write to daemon: %w", err)
	}

	// Read response (one line)
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read from daemon: %w", err)
	}

	return line, nil
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
