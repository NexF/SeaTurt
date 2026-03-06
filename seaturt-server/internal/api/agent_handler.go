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

// ModelItem represents a model entry in the API response for model listing.
type ModelItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// ListModels handles GET /api/models — returns all available models for the frontend dropdown.
func (h *AgentHandler) ListModels(c *gin.Context) {
	cfg := h.mgr.GetConfig()
	models := make([]ModelItem, 0)
	for providerName, provider := range cfg.Providers {
		for _, m := range provider.Models {
			name := m.Name
			if name == "" {
				name = m.ID
			}
			models = append(models, ModelItem{
				ID:       m.ID,
				Name:     name,
				Provider: providerName,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"models":         models,
		"default_model":  cfg.DefaultModel,
	})
}

// DesktopInfo represents the desktop status in the API response.
type DesktopInfo struct {
	KasmVNCPort string `json:"kasmvnc_port,omitempty"`
	KasmVNCURL  string `json:"kasmvnc_url,omitempty"`
	Status      string `json:"status"`
}

// GetDesktop handles GET /api/agents/:id/desktop — returns desktop status and KasmVNC URLs.
func (h *AgentHandler) GetDesktop(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	info := DesktopInfo{
		Status: string(ag.Status),
	}

	if ag.Status == agent.StatusRunning {
		// Query live port mappings
		portMap, err := h.mgr.GetMappedPorts(c.Request.Context(), ag)
		if err == nil {
			if hp, ok := portMap["3000"]; ok {
				info.KasmVNCPort = hp
				info.KasmVNCURL = "http://localhost:" + hp
			}
		}
	}

	c.JSON(http.StatusOK, info)
}
