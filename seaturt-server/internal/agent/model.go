package agent

import (
	"time"

	"github.com/seaturt/server/internal/llm"
)

type Status string

const (
	StatusCreated Status = "created"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusError   Status = "error"
)

type MCPServerConfig struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

type AgentConfig struct {
	Model       string            `json:"model"`
	MCPServers  []MCPServerConfig `json:"mcp_servers"`
	ExtraMounts []string          `json:"extra_mounts,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
}

type Agent struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Status        Status      `json:"status"`
	ContainerID   string      `json:"container_id"`
	Image         string      `json:"image"`
	WorkspacePath string      `json:"workspace_path"`
	Config        AgentConfig `json:"config"`
	KasmVNCPort   string      `json:"kasmvnc_port,omitempty"`
	KasmVNCURL    string      `json:"kasmvnc_url,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type Message struct {
	ID         string      `json:"id"`
	AgentID    string      `json:"agent_id"`
	Role       string      `json:"role"` // user | assistant | tool
	Content    llm.Content `json:"content"`
	ToolCalls  string      `json:"tool_calls,omitempty"`   // JSON string of []ToolCall
	ToolCallID string      `json:"tool_call_id,omitempty"` // For tool messages: which tool_call this responds to
	CreatedAt  time.Time   `json:"created_at"`
}
