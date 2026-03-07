package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"

	"gopkg.in/yaml.v3"
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
// It coordinates container, MCP registry, and router for each agent.
type Manager struct {
	mu        sync.RWMutex
	cfg       *config.Config
	store     Store
	docker    *container.Manager
	llmClient *llm.Client

	// Per-agent runtime state (only for running agents)
	registries map[string]*mcp.ToolRegistry // agent_id -> registry
	routers    map[string]ToolRouter        // agent_id -> router (ToolRouter interface)

	// Per-agent active chat session cancel functions.
	// Key: agentID, Value: context.CancelFunc for the running RunLoop.
	activeSessions map[string]context.CancelFunc

	// Per-agent active tool call cancel functions.
	// Key: agentID, Value: map[toolCallID]context.CancelFunc
	activeToolCalls map[string]map[string]context.CancelFunc
}

// NewManager creates a new Agent Manager.
func NewManager(cfg *config.Config, s Store, docker *container.Manager, llmClient *llm.Client) *Manager {
	return &Manager{
		cfg:             cfg,
		store:           s,
		docker:          docker,
		llmClient:       llmClient,
		registries:      make(map[string]*mcp.ToolRegistry),
		routers:         make(map[string]ToolRouter),
		activeSessions:  make(map[string]context.CancelFunc),
		activeToolCalls: make(map[string]map[string]context.CancelFunc),
	}
}

// SetActiveSession registers the cancel function for an agent's active chat session.
func (m *Manager) SetActiveSession(agentID string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.activeSessions[agentID]; ok {
		existing()
	}
	m.activeSessions[agentID] = cancel
}

// ClearActiveSession removes the cancel function for an agent's active chat session.
func (m *Manager) ClearActiveSession(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeSessions, agentID)
}

// CancelActiveSession cancels the active chat session for an agent.
// Returns true if there was an active session to cancel.
func (m *Manager) CancelActiveSession(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.activeSessions[agentID]; ok {
		cancel()
		delete(m.activeSessions, agentID)
		delete(m.activeToolCalls, agentID)
		return true
	}
	return false
}

// SetActiveToolCall registers a cancel function for a specific tool call.
func (m *Manager) SetActiveToolCall(agentID, toolCallID string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeToolCalls[agentID] == nil {
		m.activeToolCalls[agentID] = make(map[string]context.CancelFunc)
	}
	m.activeToolCalls[agentID][toolCallID] = cancel
}

// ClearActiveToolCall removes the cancel function for a specific tool call.
func (m *Manager) ClearActiveToolCall(agentID, toolCallID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if calls, ok := m.activeToolCalls[agentID]; ok {
		delete(calls, toolCallID)
	}
}

// CancelActiveToolCall cancels a specific tool call for an agent.
// Returns true if the tool call was found and cancelled.
func (m *Manager) CancelActiveToolCall(agentID, toolCallID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if calls, ok := m.activeToolCalls[agentID]; ok {
		if cancel, ok := calls[toolCallID]; ok {
			cancel()
			delete(calls, toolCallID)
			return true
		}
	}
	return false
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
		// Desktop port (always available — unified desktop image, Selkies WebRTC)
		if hp, ok := portMap["3000"]; ok {
			ag.DesktopPort = hp
			ag.DesktopURL = fmt.Sprintf("http://localhost:%s", hp)
		}
	}

	// Load MCP servers: copy binaries from host → discover tools → write YAML
	toolsDir := filepath.Join(workspacePath, ".seaturt", "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		_ = m.docker.RemoveContainer(ctx, containerID)
		return nil, fmt.Errorf("create tools dir: %w", err)
	}

	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	mcpBinsDir := m.cfg.GetMCPBinsDir()
	if err := m.loadMCPServers(ctx, containerID, mcpBinsDir, containerToolsDir, toolsDir); err != nil {
		slog.Warn("failed to load some MCP servers", "err", err)
	}

	// Load tools from YAML (no MCP processes started)
	registry := mcp.NewToolRegistry()
	if err := registry.LoadFromDir(toolsDir); err != nil {
		_ = m.docker.RemoveContainer(ctx, containerID)
		return nil, fmt.Errorf("load tools: %w", err)
	}

	// Create Executor (saves docker manager + container ID + tools dir path inside container)
	executor := mcp.NewExecutor(m.docker, containerID, containerToolsDir)

	// Create Router
	router := mcp.NewRouter(registry, executor)

	// Save runtime state
	m.mu.Lock()
	m.registries[agentID] = registry
	m.routers[agentID] = router
	m.mu.Unlock()

	ag.Status = StatusRunning
	ag.UpdatedAt = time.Now()

	// Persist to DB
	if err := m.store.CreateAgent(ag); err != nil {
		_ = m.docker.RemoveContainer(ctx, containerID)
		m.mu.Lock()
		delete(m.registries, agentID)
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

		// Container is running — load tools from YAML (no MCP processes started)
		if ag.Status != StatusRunning {
			_ = m.store.UpdateAgentStatus(ag.ID, StatusRunning)
		}

		toolsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "tools")
		registry := mcp.NewToolRegistry()
		if err := registry.LoadFromDir(toolsDir); err != nil {
			slog.Warn("agent container running but tools load failed, marking error",
				"id", ag.ID, "name", ag.Name, "err", err)
			_ = m.store.UpdateAgentStatus(ag.ID, StatusError)
			continue
		}

		containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
		executor := mcp.NewExecutor(m.docker, ag.ContainerID, containerToolsDir)
		router := mcp.NewRouter(registry, executor)

		m.mu.Lock()
		m.registries[ag.ID] = registry
		m.routers[ag.ID] = router
		m.mu.Unlock()

		slog.Info("agent state synced",
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
	// Re-load MCP servers (may have new bins since last start)
	toolsDir := filepath.Join(ag.WorkspacePath, ".seaturt", "tools")
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	mcpBinsDir := m.cfg.GetMCPBinsDir()
	if err := m.loadMCPServers(ctx, ag.ContainerID, mcpBinsDir, containerToolsDir, toolsDir); err != nil {
		slog.Warn("failed to load some MCP servers on start", "err", err)
	}

	registry := mcp.NewToolRegistry()
	if err := registry.LoadFromDir(toolsDir); err != nil {
		return fmt.Errorf("load tools: %w", err)
	}

	executor := mcp.NewExecutor(m.docker, ag.ContainerID, containerToolsDir)
	router := mcp.NewRouter(registry, executor)

	m.mu.Lock()
	m.registries[id] = registry
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

	// Clean up runtime state (no long-lived connections to close)
	m.mu.Lock()
	delete(m.registries, id)
	delete(m.routers, id)
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

	// Clean up runtime state (no long-lived connections to close)
	m.mu.Lock()
	delete(m.registries, id)
	delete(m.routers, id)
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
func (m *Manager) GetRouter(id string) ToolRouter {
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

// containerMCPDiscoverTimeout is the timeout for running tools/list on a single MCP server.
const containerMCPDiscoverTimeout = 10 * time.Second

// loadMCPServers scans srcDir for MCP binaries/scripts, copies each to the container,
// discovers tools via MCP protocol, and writes YAML files.
// This method is used by both Create() and Start().
// Future hot-reload APIs can also call this method.
func (m *Manager) loadMCPServers(ctx context.Context, containerID, srcDir, containerToolsDir, hostToolsDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("mcp-bins directory not found, skipping MCP loading", "dir", srcDir)
			return nil
		}
		return fmt.Errorf("read mcp-bins dir: %w", err)
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == ".gitkeep" {
			continue
		}
		srcPath := filepath.Join(srcDir, entry.Name())
		if err := m.discoverAndLoadSingleMCP(ctx, containerID, srcPath, containerToolsDir, hostToolsDir); err != nil {
			slog.Warn("failed to load MCP server, skipping",
				"file", entry.Name(), "err", err)
			continue
		}
		loaded++
	}

	slog.Info("MCP servers loaded", "total", loaded, "src_dir", srcDir)
	return nil
}

// discoverAndLoadSingleMCP handles the complete lifecycle for a single MCP server:
//  1. Copy binary/script to container toolsDir
//  2. Discover tools via MCP protocol (initialize + tools/list)
//  3. Write YAML to both container and host toolsDir
//
// This method can be called individually for future hot-reload support.
func (m *Manager) discoverAndLoadSingleMCP(ctx context.Context, containerID, srcPath, containerToolsDir, hostToolsDir string) error {
	fileName := filepath.Base(srcPath)
	serverName := deriveMCPServerName(fileName)

	// 1. Copy binary/script to container
	if err := m.docker.CopyToContainer(ctx, containerID, srcPath, containerToolsDir); err != nil {
		return fmt.Errorf("copy %s to container: %w", fileName, err)
	}

	// 2. Discover tools via MCP protocol
	binPath := filepath.Join(containerToolsDir, fileName)
	tools, serverDesc, err := m.discoverTools(ctx, containerID, binPath, fileName)
	if err != nil {
		return fmt.Errorf("discover tools for %s: %w", serverName, err)
	}

	slog.Info("discovered MCP tools",
		"server", serverName,
		"tools_count", len(tools),
	)

	// 3. Write YAML to host toolsDir (which is bind-mounted as /workspace/.seaturt/tools/)
	if err := m.writeToolsYAML(hostToolsDir, serverName, fileName, serverDesc, tools); err != nil {
		return fmt.Errorf("write YAML for %s: %w", serverName, err)
	}

	return nil
}

// discoverTools runs a MCP server binary inside the container, performs the
// initialize + tools/list handshake, and returns the discovered tool definitions.
func (m *Manager) discoverTools(ctx context.Context, containerID, binPath, fileName string) ([]mcp.ToolDefinition, string, error) {
	// Determine execution command based on file extension
	var cmd []string
	if strings.HasSuffix(fileName, ".py") {
		cmd = []string{"python3", binPath}
	} else {
		cmd = []string{binPath}
	}

	discoverCtx, cancel := context.WithTimeout(ctx, containerMCPDiscoverTimeout)
	defer cancel()

	hijacked, err := m.docker.ExecStdio(discoverCtx, containerID, container.ExecAttachOptions{
		Cmd: cmd,
	})
	if err != nil {
		return nil, "", fmt.Errorf("exec stdio for discover: %w", err)
	}
	defer hijacked.Close()

	// Build ephemeral transport
	transport := mcp.NewDiscoverTransport(hijacked)
	defer transport.Close()

	// Initialize handshake
	initResult, err := transport.InitializeAndGetResult()
	if err != nil {
		return nil, "", fmt.Errorf("initialize: %w", err)
	}

	serverDesc := ""
	if initResult != nil && initResult.ServerInfo.Name != "" {
		serverDesc = initResult.ServerInfo.Name + " v" + initResult.ServerInfo.Version
	}

	// tools/list
	tools, err := transport.ToolsList()
	if err != nil {
		return nil, "", fmt.Errorf("tools/list: %w", err)
	}

	return tools, serverDesc, nil
}

// writeToolsYAML generates a YAML file for the discovered MCP server and writes it to the host tools dir.
func (m *Manager) writeToolsYAML(hostToolsDir, serverName, command, description string, tools []mcp.ToolDefinition) error {
	if err := os.MkdirAll(hostToolsDir, 0755); err != nil {
		return fmt.Errorf("mkdir tools dir: %w", err)
	}

	// Build YAML-friendly structure
	type yamlTool struct {
		Name        string         `yaml:"name"`
		Description string         `yaml:"description"`
		InputSchema map[string]any `yaml:"inputSchema,omitempty"`
	}
	type yamlServer struct {
		Name        string     `yaml:"name"`
		Command     string     `yaml:"command"`
		Description string     `yaml:"description"`
		Enabled     bool       `yaml:"enabled"`
		Tools       []yamlTool `yaml:"tools"`
	}

	ysd := yamlServer{
		Name:        serverName,
		Command:     command,
		Description: description,
		Enabled:     true,
		Tools:       make([]yamlTool, 0, len(tools)),
	}

	for _, t := range tools {
		schema, _ := toMapStringAny(t.InputSchema)
		ysd.Tools = append(ysd.Tools, yamlTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	data, err := yaml.Marshal(ysd)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}

	yamlPath := filepath.Join(hostToolsDir, serverName+".yaml")
	if err := os.WriteFile(yamlPath, data, 0644); err != nil {
		return fmt.Errorf("write yaml: %w", err)
	}

	slog.Debug("wrote MCP tools YAML", "path", yamlPath, "tools", len(tools))
	return nil
}

// deriveMCPServerName derives the MCP server name from a binary filename.
// Rules:
//  1. Remove "mcp-server-" prefix (if present)
//  2. Remove file extension (.py, etc.)
//
// Examples:
//
//	"mcp-server-core"    → "core"
//	"mcp-server-desktop" → "desktop"
//	"my-custom-tool"     → "my-custom-tool"
//	"web-search.py"      → "web-search"
func deriveMCPServerName(fileName string) string {
	name := fileName
	// Remove file extension
	ext := filepath.Ext(name)
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	// Remove "mcp-server-" prefix
	name = strings.TrimPrefix(name, "mcp-server-")
	return name
}

// toMapStringAny converts an any value to map[string]any.
func toMapStringAny(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	// Try JSON round-trip for types like map[string]interface{} from different packages
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, false
	}
	return result, true
}

// generateID creates a unique agent ID.
func generateID() string {
	return fmt.Sprintf("agent_%d", time.Now().UnixNano())
}
