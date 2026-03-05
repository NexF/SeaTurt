package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/seaturt/server/internal/agent"
	"github.com/gin-gonic/gin"
)

// AgentHandler handles Agent management API endpoints.
type AgentHandler struct {
	mgr *agent.Manager
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(mgr *agent.Manager) *AgentHandler {
	return &AgentHandler{mgr: mgr}
}

// CreateAgent handles POST /api/agents
func (h *AgentHandler) CreateAgent(c *gin.Context) {
	var req agent.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	ag, err := h.mgr.Create(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, ag)
}

// ListAgents handles GET /api/agents
func (h *AgentHandler) ListAgents(c *gin.Context) {
	agents, err := h.mgr.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if agents == nil {
		agents = []*agent.Agent{}
	}

	c.JSON(http.StatusOK, agents)
}

// GetAgent handles GET /api/agents/:id
func (h *AgentHandler) GetAgent(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	c.JSON(http.StatusOK, ag)
}

// StartAgent handles POST /api/agents/:id/start
func (h *AgentHandler) StartAgent(c *gin.Context) {
	id := c.Param("id")

	if err := h.mgr.Start(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

// StopAgent handles POST /api/agents/:id/stop
func (h *AgentHandler) StopAgent(c *gin.Context) {
	id := c.Param("id")

	if err := h.mgr.Stop(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// DeleteAgent handles DELETE /api/agents/:id
func (h *AgentHandler) DeleteAgent(c *gin.Context) {
	id := c.Param("id")

	if err := h.mgr.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// PortInfo represents a single port mapping entry in the API response.
type PortInfo struct {
	HostPort    string `json:"host_port"`
	Description string `json:"description"`
}

// GetPorts handles GET /api/agents/:id/ports — returns the port mapping table.
func (h *AgentHandler) GetPorts(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	portMap, err := h.mgr.GetMappedPorts(c.Request.Context(), ag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Build response with descriptions
	ports := make(map[string]PortInfo)
	for containerPort, hostPort := range portMap {
		ports[containerPort] = PortInfo{
			HostPort:    hostPort,
			Description: agent.GetPortDescription(containerPort),
		}
	}

	c.JSON(http.StatusOK, gin.H{"ports": ports})
}

// GetSystemPrompt handles GET /api/agents/:id/system-prompt
func (h *AgentHandler) GetSystemPrompt(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	prompt := h.mgr.LoadSystemPrompt(ag)
	c.JSON(http.StatusOK, gin.H{"system_prompt": prompt})
}

// UpdateSystemPrompt handles PUT /api/agents/:id/system-prompt
func (h *AgentHandler) UpdateSystemPrompt(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Write directly to SYSTEM.md
	seaturtDir := filepath.Join(ag.WorkspacePath, ".seaturt")
	_ = os.MkdirAll(seaturtDir, 0755)
	if err := os.WriteFile(filepath.Join(seaturtDir, "SYSTEM.md"), body, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}
