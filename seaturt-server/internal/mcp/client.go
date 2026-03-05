package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/seaturt/server/internal/container"
)

// Client is an MCP client that communicates with a single MCP server
// via a stdio transport (docker exec).
type Client struct {
	name      string // MCP server name, e.g. "core"
	transport *Transport
	nextID    atomic.Int64
	tools     []ToolDefinition // cached after Initialize + ListTools
}

// ClientConfig holds the configuration for creating an MCP Client.
type ClientConfig struct {
	Name        string // logical name, e.g. "core", "browser"
	ContainerID string
	Command     string // binary name, e.g. "mcp-server-core"
}

// NewClient creates an MCP client, establishes the transport via docker exec,
// performs the initialize handshake, and caches the available tools.
func NewClient(ctx context.Context, dockerMgr *container.Manager, cfg ClientConfig) (*Client, error) {
	transport, err := NewTransport(ctx, dockerMgr, TransportConfig{
		ContainerID: cfg.ContainerID,
		Command:     cfg.Command,
	})
	if err != nil {
		return nil, fmt.Errorf("create transport for %s: %w", cfg.Name, err)
	}

	c := &Client{
		name:      cfg.Name,
		transport: transport,
	}

	// Perform MCP handshake
	if _, err := c.Initialize(); err != nil {
		transport.Close()
		return nil, fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}

	// Cache available tools
	tools, err := c.ListTools()
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("list tools for %s: %w", cfg.Name, err)
	}
	c.tools = tools

	slog.Info("mcp client connected",
		"name", cfg.Name,
		"command", cfg.Command,
		"tools", len(tools),
	)

	return c, nil
}

// Name returns the logical name of this MCP server.
func (c *Client) Name() string {
	return c.name
}

// Tools returns the cached tool definitions from this MCP server.
func (c *Client) Tools() []ToolDefinition {
	return c.tools
}

// Initialize performs the MCP initialize handshake.
func (c *Client) Initialize() (*InitializeResult, error) {
	id := c.nextID.Add(1)

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
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.transport.Send(data)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	// Send notifications/initialized
	notif, err := NewNotification("notifications/initialized")
	if err != nil {
		return nil, fmt.Errorf("build notification: %w", err)
	}
	if err := c.transport.SendNotification(notif); err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}

	return &result, nil
}

// ListTools calls tools/list and returns the tool definitions.
func (c *Client) ListTools() ([]ToolDefinition, error) {
	id := c.nextID.Add(1)

	data, err := NewRequest(id, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.transport.Send(data)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the MCP server and returns the result.
func (c *Client) CallTool(name string, args map[string]any) (*CallToolResult, error) {
	id := c.nextID.Add(1)

	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	data, err := NewRequest(id, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.transport.Send(data)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	return &result, nil
}

// Close terminates the MCP client and its transport.
func (c *Client) Close() error {
	slog.Info("mcp client closing", "name", c.name)
	return c.transport.Close()
}
