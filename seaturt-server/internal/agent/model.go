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
	Provider    string            `json:"provider"`
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
	DesktopPort   string      `json:"desktop_port,omitempty"`
	DesktopURL    string      `json:"desktop_url,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type Session struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID               string      `json:"id"`
	AgentID          string      `json:"agent_id"`
	SessionID        string      `json:"session_id"`
	Role             string      `json:"role"` // user | assistant | tool
	Content          llm.Content `json:"content"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	ToolCalls        string      `json:"tool_calls,omitempty"`   // JSON string of []ToolCall
	ToolCallID       string      `json:"tool_call_id,omitempty"` // For tool messages: which tool_call this responds to
	CreatedAt        time.Time   `json:"created_at"`
}

// CronJob represents a scheduled task bound to an Agent.
// Type "cron" uses CronExpr for periodic execution; type "at" uses RunAt for one-shot execution.
type CronJob struct {
	ID              string     `json:"id"`               // cron_{nano_timestamp}
	AgentID         string     `json:"agent_id"`
	Type            string     `json:"type"`             // "cron" | "at"
	CronExpr        string     `json:"cron_expr"`        // cron type: 5-field expression; at type: empty
	RunAt           *time.Time `json:"run_at"`           // at type: one-shot time (RFC 3339); cron type: nil
	Prompt          string     `json:"prompt"`           // prompt sent to Agent on trigger
	SessionStrategy string     `json:"session_strategy"` // "fixed" | "new"
	SessionID       string     `json:"session_id"`       // fixed strategy: bound Session ID
	Enabled         bool       `json:"enabled"`          // at type auto-disables after execution
	LastRunAt       *time.Time `json:"last_run_at"`
	NextRunAt       *time.Time `json:"next_run_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// CronJobExecution records a single execution of a CronJob.
type CronJobExecution struct {
	ID        string    `json:"id"`          // exec_{nano_timestamp}
	CronJobID string    `json:"cron_job_id"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`    // "success" | "failed" | "skipped"
	Error     string    `json:"error"`     // error details if failed
	Duration  int64     `json:"duration"`  // milliseconds
	StartedAt time.Time `json:"started_at"`
	CreatedAt time.Time `json:"created_at"`
}
