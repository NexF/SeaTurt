package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/seaturt/server/internal/container"
)

// Pool manages multiple MCP clients for a single agent's container.
// Each MCP server (core, browser, git, etc.) has its own client.
type Pool struct {
	mu      sync.RWMutex
	clients map[string]*Client // name -> client
}

// NewPool creates an empty MCP client pool.
func NewPool() *Pool {
	return &Pool{
		clients: make(map[string]*Client),
	}
}

// MCPServerDef defines an MCP server to connect to within a container.
type MCPServerDef struct {
	Name    string // logical name, e.g. "core"
	Command string // binary, e.g. "mcp-server-core"
}

const (
	mcpConnectMaxRetries    = 10
	mcpConnectInitialDelay  = 500 * time.Millisecond
	mcpConnectMaxDelay      = 5 * time.Second
	mcpConnectBackoffFactor = 1.5
)

// Connect establishes MCP client connections for the given server definitions.
// Each server gets its own docker exec session and MCP handshake.
// It retries with exponential backoff to wait for the container's MCP servers to become ready.
func (p *Pool) Connect(ctx context.Context, dockerMgr *container.Manager, containerID string, servers []MCPServerDef) error {
	for _, srv := range servers {
		client, err := p.connectWithRetry(ctx, dockerMgr, containerID, srv)
		if err != nil {
			p.CloseAll()
			return fmt.Errorf("connect mcp server %s: %w", srv.Name, err)
		}

		p.mu.Lock()
		p.clients[srv.Name] = client
		p.mu.Unlock()
	}

	slog.Info("mcp pool connected",
		"container_id", containerID[:12],
		"servers", len(servers),
	)
	return nil
}

// connectWithRetry attempts to connect to a single MCP server with exponential backoff.
func (p *Pool) connectWithRetry(ctx context.Context, dockerMgr *container.Manager, containerID string, srv MCPServerDef) (*Client, error) {
	delay := mcpConnectInitialDelay
	var lastErr error

	for attempt := 1; attempt <= mcpConnectMaxRetries; attempt++ {
		client, err := NewClient(ctx, dockerMgr, ClientConfig{
			Name:        srv.Name,
			ContainerID: containerID,
			Command:     srv.Command,
		})
		if err == nil {
			if attempt > 1 {
				slog.Info("mcp server connected after retry",
					"name", srv.Name,
					"attempt", attempt,
				)
			}
			return client, nil
		}

		lastErr = err
		if attempt == mcpConnectMaxRetries {
			break
		}

		slog.Warn("mcp server not ready, retrying",
			"name", srv.Name,
			"attempt", attempt,
			"delay", delay,
			"err", err,
		)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for %s: %w", srv.Name, ctx.Err())
		case <-time.After(delay):
		}

		delay = time.Duration(float64(delay) * mcpConnectBackoffFactor)
		if delay > mcpConnectMaxDelay {
			delay = mcpConnectMaxDelay
		}
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", mcpConnectMaxRetries, lastErr)
}

// Get returns the MCP client for the given server name.
func (p *Pool) Get(name string) (*Client, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.clients[name]
	return c, ok
}

// AllClients returns all MCP clients in the pool.
func (p *Pool) AllClients() []*Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		result = append(result, c)
	}
	return result
}

// AllTools returns all tool definitions from all MCP servers in the pool.
func (p *Pool) AllTools() []ToolDefinition {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var tools []ToolDefinition
	for _, c := range p.clients {
		tools = append(tools, c.Tools()...)
	}
	return tools
}

// CloseAll terminates all MCP client connections in the pool.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, c := range p.clients {
		if err := c.Close(); err != nil {
			slog.Warn("failed to close mcp client", "name", name, "err", err)
		}
	}
	p.clients = make(map[string]*Client)
	slog.Info("mcp pool closed")
}
