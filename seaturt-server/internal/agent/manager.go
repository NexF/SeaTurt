package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"
)

// Store defines the persistence operations needed by Manager.
type Store interface {
	CreateAgent(a *Agent) error
	GetAgent(id string) (*Agent, error)
	ListAgents() ([]*Agent, error)
	UpdateAgentStatus(id string, status Status) error
	DeleteAgent(id string) error
	CreateMessage(m *Message) error
	ListMessages(agentID string) ([]*Message, error)
	DeleteMessages(agentID string) error
}

// Manager manages Agent lifecycle: create, start, stop, delete.
// It coordinates container, MCP pool, and router for each agent.
type Manager struct {
	mu        sync.RWMutex
	cfg       *config.Config
	store     Store
	docker    *container.Manager
	llmClient *llm.Client

	// Per-agent runtime state (only for running agents)
	pools   map[string]*mcp.Pool   // agent_id -> pool
	routers map[string]*mcp.Router // agent_id -> router
}

// NewManager creates a new Agent Manager.
func NewManager(cfg *config.Config, s Store, docker *container.Manager, llmClient *llm.Client) *Manager {
	return &Manager{
		cfg:       cfg,
		store:     s,
		docker:    docker,
		llmClient: llmClient,
		pools:     make(map[string]*mcp.Pool),
		routers:   make(map[string]*mcp.Router),
	}
}

// CreateAgentRequest is the input for creating a new agent.
type CreateAgentRequest struct {
	Name         string            `json:"name"`
	Model        string            `json:"model,omitempty"`
	MCPServers   []MCPServerConfig `json:"mcp_servers,omitempty"`
	ExtraMounts  []string          `json:"extra_mounts,omitempty"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
}

// hasMCPServer checks if a named MCP server exists in the list.
func hasMCPServer(servers []MCPServerConfig, name string) bool {
	for _, s := range servers {
		if s.Name == name {
			return true
		}
	}
	return false
}

// Create creates a new agent: persists to DB, creates container, starts it, establishes MCP connections.
func (m *Manager) Create(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	agentID := generateID()
	now := time.Now()

	// Determine MCP servers
	mcpServers := req.MCPServers
	if len(mcpServers) == 0 {
		for _, s := range m.cfg.DefaultMCPServers {
			mcpServers = append(mcpServers, MCPServerConfig{Name: s.Name, Command: s.Command})
		}
	}

	// Always include desktop MCP server (unified desktop image)
	if !hasMCPServer(mcpServers, "desktop") {
		mcpServers = append(mcpServers, MCPServerConfig{Name: "desktop", Command: "mcp-server-desktop"})
	}

	// Determine model
	model := req.Model
	if model == "" {
		model = m.cfg.DefaultModel
	}

	// Unified image (no more desktop/non-desktop distinction)
	agentImage := m.cfg.SandboxImage

	// Workspace path
	workspacePath := filepath.Join(m.cfg.WorkspaceRoot, agentID)
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	// Create .seaturt/ directory
	seaturtDir := filepath.Join(workspacePath, ".seaturt")
	if err := os.MkdirAll(seaturtDir, 0755); err != nil {
		return nil, fmt.Errorf("create .seaturt dir: %w", err)
	}

	// Generate and write SYSTEM.md
	systemMD := GenerateSystemMD(SystemPromptConfig{
		MCPServers: mcpServers,
		EnvVars:    req.EnvVars,
		ExtraRules: req.SystemPrompt,
	})
	if err := os.WriteFile(filepath.Join(seaturtDir, "SYSTEM.md"), []byte(systemMD), 0644); err != nil {
		slog.Warn("failed to write SYSTEM.md", "err", err)
	}

	ag := &Agent{
		ID:            agentID,
		Name:          req.Name,
		Status:        StatusCreated,
		Image:         agentImage,
		WorkspacePath: workspacePath,
		Config: AgentConfig{
			Model:       model,
			MCPServers:  mcpServers,
			ExtraMounts: req.ExtraMounts,
			EnvVars:     req.EnvVars,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// ShmSize always set (browser rendering needs it)
	shmSize := m.cfg.Container.ShmSize

	// Create container
	containerID, err := m.docker.CreateContainer(ctx, container.CreateContainerOpts{
		AgentID:       agentID,
		Image:         agentImage,
		WorkspacePath: workspacePath,
		ExtraMounts:   req.ExtraMounts,
		EnvVars:       req.EnvVars,
		ShmSize:       shmSize,
	})
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}
	ag.ContainerID = containerID

	// Start container
	if err := m.docker.StartContainer(ctx, containerID); err != nil {
		_ = m.docker.RemoveContainer(ctx, containerID)
		return nil, fmt.Errorf("start container: %w", err)
	}

	// Query actual port mappings and generate PORTS.md
	portMap, err := m.docker.GetMappedPorts(ctx, containerID)
	if err != nil {
		slog.Warn("failed to get mapped ports", "err", err)
	} else if len(portMap) > 0 {
		portsMD := GeneratePortsMD(portMap)
		if err := os.WriteFile(filepath.Join(seaturtDir, "PORTS.md"), []byte(portsMD), 0644); err != nil {
			slog.Warn("failed to write PORTS.md", "err", err)
		}
		// KasmVNC port (always available — unified desktop image)
		if hp, ok := portMap["3000"]; ok {
			ag.KasmVNCPort = hp
			ag.KasmVNCURL = fmt.Sprintf("http://localhost:%s", hp)
		}
	}

	// Establish MCP connections
	pool := mcp.NewPool()
	var serverDefs []mcp.MCPServerDef
	for _, s := range mcpServers {
		serverDefs = append(serverDefs, mcp.MCPServerDef{Name: s.Name, Command: s.Command})
	}

	if err := pool.Connect(ctx, m.docker, containerID, serverDefs); err != nil {
		_ = m.docker.RemoveContainer(ctx, containerID)
		return nil, fmt.Errorf("connect mcp: %w", err)
	}

	router := mcp.NewRouter(pool)

	// Save runtime state
	m.mu.Lock()
	m.pools[agentID] = pool
	m.routers[agentID] = router
	m.mu.Unlock()

	ag.Status = StatusRunning
	ag.UpdatedAt = time.Now()

	// Persist to DB
	if err := m.store.CreateAgent(ag); err != nil {
		pool.CloseAll()
		_ = m.docker.RemoveContainer(ctx, containerID)
		m.mu.Lock()
		delete(m.pools, agentID)
		delete(m.routers, agentID)
		m.mu.Unlock()
		return nil, fmt.Errorf("save agent: %w", err)
	}

	slog.Info("agent created",
		"id", agentID,
		"name", req.Name,
		"container", containerID[:12],
		"mcp_servers", len(mcpServers),
		"tools", len(router.AllTools()),
		"image", agentImage,
	)

	return ag, nil
}

// SyncAgentStates reconciles DB agent states with actual Docker container states.
// Should be called once at startup. The logic mirrors Docker semantics:
//   - Container running  → Agent running (re-establish MCP)
//   - Container stopped  → Agent stopped
//   - Container removed  → Delete Agent from DB
func (m *Manager) SyncAgentStates(ctx context.Context) {
	agents, err := m.store.ListAgents()
	if err != nil {
		slog.Error("sync agent states: failed to list agents", "err", err)
		return
	}

	for _, ag := range agents {
		if ag.ContainerID == "" {
			// No container — orphan record, clean up
			slog.Warn("agent has no container, deleting",
				"id", ag.ID, "name", ag.Name)
			_ = m.store.DeleteMessages(ag.ID)
			_ = m.store.DeleteAgent(ag.ID)
			continue
		}

		cs, err := m.docker.InspectContainer(ctx, ag.ContainerID)
		if err != nil {
			// Container doesn't exist (docker rm) → delete agent
			slog.Warn("agent container removed, deleting agent",
				"id", ag.ID, "name", ag.Name, "container_id", ag.ContainerID[:12])
			_ = m.store.DeleteMessages(ag.ID)
			_ = m.store.DeleteAgent(ag.ID)
			continue
		}

		if !cs.Running {
			// Container exists but stopped
			if ag.Status != StatusStopped {
				slog.Info("agent container stopped, syncing status",
					"id", ag.ID, "name", ag.Name, "container_status", cs.Status)
				_ = m.store.UpdateAgentStatus(ag.ID, StatusStopped)
			}
			continue
		}

		// Container is running — re-establish MCP connections
		if ag.Status != StatusRunning {
			_ = m.store.UpdateAgentStatus(ag.ID, StatusRunning)
		}

		pool := mcp.NewPool()
		var serverDefs []mcp.MCPServerDef
		for _, s := range ag.Config.MCPServers {
			serverDefs = append(serverDefs, mcp.MCPServerDef{Name: s.Name, Command: s.Command})
		}

		if err := pool.Connect(ctx, m.docker, ag.ContainerID, serverDefs); err != nil {
			slog.Warn("agent container running but MCP reconnect failed, marking error",
				"id", ag.ID, "name", ag.Name, "err", err)
			pool.CloseAll()
			_ = m.store.UpdateAgentStatus(ag.ID, StatusError)
			continue
		}

		router := mcp.NewRouter(pool)

		m.mu.Lock()
		m.pools[ag.ID] = pool
		m.routers[ag.ID] = router
		m.mu.Unlock()

		slog.Info("agent state synced, reconnected",
			"id", ag.ID, "name", ag.Name,
			"tools", len(router.AllTools()),
		)
	}
}

// Get returns an agent by ID.
func (m *Manager) Get(id string) (*Agent, error) {
	return m.store.GetAgent(id)
}

// List returns all agents.
func (m *Manager) List() ([]*Agent, error) {
	return m.store.ListAgents()
}

// Start starts a stopped agent's container and re-establishes MCP connections.
func (m *Manager) Start(ctx context.Context, id string) error {
	ag, err := m.store.GetAgent(id)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}

	if ag.Status == StatusRunning {
		return fmt.Errorf("agent already running")
	}

	if ag.ContainerID == "" {
		return fmt.Errorf("agent has no container")
	}

	// Start container
	if err := m.docker.StartContainer(ctx, ag.ContainerID); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	// Regenerate PORTS.md (port mappings may change after restart)
	portMap, err := m.docker.GetMappedPorts(ctx, ag.ContainerID)
	if err != nil {
		slog.Warn("failed to get mapped ports on start", "err", err)
	} else if len(portMap) > 0 {
		seaturtDir := filepath.Join(ag.WorkspacePath, ".seaturt")
		_ = os.MkdirAll(seaturtDir, 0755)
		portsMD := GeneratePortsMD(portMap)
		if err := os.WriteFile(filepath.Join(seaturtDir, "PORTS.md"), []byte(portsMD), 0644); err != nil {
			slog.Warn("failed to write PORTS.md on start", "err", err)
		}
	}
	// Re-establish MCP connections
	pool := mcp.NewPool()
	var serverDefs []mcp.MCPServerDef
	for _, s := range ag.Config.MCPServers {
		serverDefs = append(serverDefs, mcp.MCPServerDef{Name: s.Name, Command: s.Command})
	}

	if err := pool.Connect(ctx, m.docker, ag.ContainerID, serverDefs); err != nil {
		return fmt.Errorf("reconnect mcp: %w", err)
	}

	router := mcp.NewRouter(pool)

	m.mu.Lock()
	// Close old pool if any
	if old, ok := m.pools[id]; ok {
		old.CloseAll()
	}
	m.pools[id] = pool
	m.routers[id] = router
	m.mu.Unlock()

	if err := m.store.UpdateAgentStatus(id, StatusRunning); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	slog.Info("agent started", "id", id)
	return nil
}

// Stop stops an agent's container and cleans up MCP connections.
func (m *Manager) Stop(ctx context.Context, id string) error {
	ag, err := m.store.GetAgent(id)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}

	if ag.Status == StatusStopped {
		return fmt.Errorf("agent already stopped")
	}

	// Close MCP connections
	m.mu.Lock()
	if pool, ok := m.pools[id]; ok {
		pool.CloseAll()
		delete(m.pools, id)
		delete(m.routers, id)
	}
	m.mu.Unlock()

	// Stop container
	if ag.ContainerID != "" {
		if err := m.docker.StopContainer(ctx, ag.ContainerID); err != nil {
			slog.Warn("failed to stop container", "id", id, "err", err)
		}
	}

	if err := m.store.UpdateAgentStatus(id, StatusStopped); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	slog.Info("agent stopped", "id", id)
	return nil
}

// Delete deletes an agent: stops container, removes it, cleans up MCP connections, removes from DB.
func (m *Manager) Delete(ctx context.Context, id string) error {
	ag, err := m.store.GetAgent(id)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}

	// Close MCP connections
	m.mu.Lock()
	if pool, ok := m.pools[id]; ok {
		pool.CloseAll()
		delete(m.pools, id)
		delete(m.routers, id)
	}
	m.mu.Unlock()

	// Remove container
	if ag.ContainerID != "" {
		if err := m.docker.RemoveContainer(ctx, ag.ContainerID); err != nil {
			slog.Warn("failed to remove container", "id", id, "err", err)
		}
	}

	// Delete messages
	if err := m.store.DeleteMessages(id); err != nil {
		slog.Warn("failed to delete messages", "id", id, "err", err)
	}

	// Delete agent from DB
	if err := m.store.DeleteAgent(id); err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}

	slog.Info("agent deleted", "id", id)
	return nil
}

// GetRouter returns the MCP router for an agent. Returns nil if not running.
func (m *Manager) GetRouter(id string) *mcp.Router {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.routers[id]
}

// GetLLMClient returns the LLM client.
func (m *Manager) GetLLMClient() *llm.Client {
	return m.llmClient
}

// GetStore returns the store for message operations.
func (m *Manager) GetStore() Store {
	return m.store
}

// GetConfig returns the global config.
func (m *Manager) GetConfig() *config.Config {
	return m.cfg
}

// LoadSystemPrompt reads SYSTEM.md from the agent's workspace.
// Returns DefaultSystemPrompt if the file doesn't exist or can't be read.
func (m *Manager) LoadSystemPrompt(ag *Agent) string {
	path := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("failed to read SYSTEM.md, using default", "agent_id", ag.ID, "err", err)
		return DefaultSystemPrompt
	}
	return string(data)
}

// GetMappedPorts returns the port mapping for a running agent's container.
func (m *Manager) GetMappedPorts(ctx context.Context, ag *Agent) (map[string]string, error) {
	if ag.ContainerID == "" {
		return nil, fmt.Errorf("agent has no container")
	}
	return m.docker.GetMappedPorts(ctx, ag.ContainerID)
}

// generateID creates a unique agent ID.
func generateID() string {
	return fmt.Sprintf("agent_%d", time.Now().UnixNano())
}
