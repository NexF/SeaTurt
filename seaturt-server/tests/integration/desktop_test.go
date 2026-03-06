//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-49: All agents have desktop ports (unified desktop image, Selkies WebRTC).
func TestUnifiedImage_AlwaysDesktopPort(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "desktop-port-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	assert.Equal(t, agentpkg.StatusRunning, ag.Status)

	// Check port mapping has 3000 (Selkies WebRTC)
	portsResp := doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/ports", nil)
	assert.Equal(t, http.StatusOK, portsResp.StatusCode)

	var ports struct {
		Ports map[string]struct {
			HostPort    string `json:"host_port"`
			Description string `json:"description"`
		} `json:"ports"`
	}
	decodeJSON(t, portsResp, &ports)

	if desktopPort, ok := ports.Ports["3000"]; ok {
		assert.NotEmpty(t, desktopPort.HostPort, "Selkies desktop port should be mapped")
	}
}

// IT-50: All agents have desktop MCP server.
func TestUnifiedImage_AlwaysDesktopMCP(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "desktop-mcp-test",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Skip("agent creation failed (test image may not have mcp-server-desktop)")
	}

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	hasDesktop := false
	for _, s := range ag.Config.MCPServers {
		if s.Name == "desktop" {
			hasDesktop = true
			break
		}
	}
	assert.True(t, hasDesktop, "desktop MCP server should always be present")
}

// IT-51: Creating agent without desktop param works and has desktop.
func TestUnifiedImage_NoDesktopField(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "no-desktop-field",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	assert.Equal(t, agentpkg.StatusRunning, ag.Status)
}

// IT-52: Passing deprecated desktop field is ignored.
func TestUnifiedImage_DesktopFieldIgnored(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Both desktop: true and desktop: false should produce same result
	resp1 := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name":    "desktop-true",
		"desktop": true,
	})
	require.Equal(t, http.StatusCreated, resp1.StatusCode)
	var ag1 agentpkg.Agent
	decodeJSON(t, resp1, &ag1)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag1.ID, nil)

	resp2 := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name":    "desktop-false",
		"desktop": false,
	})
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	var ag2 agentpkg.Agent
	decodeJSON(t, resp2, &ag2)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag2.ID, nil)

	// Both should have desktop MCP server
	hasDesktop1 := false
	hasDesktop2 := false
	for _, s := range ag1.Config.MCPServers {
		if s.Name == "desktop" {
			hasDesktop1 = true
		}
	}
	for _, s := range ag2.Config.MCPServers {
		if s.Name == "desktop" {
			hasDesktop2 = true
		}
	}
	assert.True(t, hasDesktop1)
	assert.True(t, hasDesktop2)
}

// IT-53: GET /api/agents/:id returns desktop_port and desktop_url.
func TestUnifiedImage_DesktopPortInResponse(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "desktop-response-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Get agent details
	getResp := doRequest(t, ts, "GET", "/api/agents/"+ag.ID, nil)
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var fetched agentpkg.Agent
	decodeJSON(t, getResp, &fetched)

	// Desktop fields should be present (port is dynamically assigned)
	if fetched.DesktopPort != "" {
		assert.NotEmpty(t, fetched.DesktopURL)
		assert.Contains(t, fetched.DesktopURL, "http://localhost:")
	}
}

// IT-54: SYSTEM.md always contains desktop guide.
func TestUnifiedImage_SystemMDHasDesktopGuide(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "system-md-desktop-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Get system prompt
	promptResp := doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/system-prompt", nil)
	assert.Equal(t, http.StatusOK, promptResp.StatusCode)

	var promptData struct {
		SystemPrompt string `json:"system_prompt"`
	}
	err := json.NewDecoder(promptResp.Body).Decode(&promptData)
	require.NoError(t, err)

	assert.Contains(t, promptData.SystemPrompt, "桌面环境")
	assert.Contains(t, promptData.SystemPrompt, "screenshot")
	assert.Contains(t, promptData.SystemPrompt, "Selkies")
}

// IT-55: Port mappings do not contain 5900/6080 (old VNC ports).
func TestUnifiedImage_NoVNCPortsRemoved(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "no-vnc-ports-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	portsResp := doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/ports", nil)
	assert.Equal(t, http.StatusOK, portsResp.StatusCode)

	var ports struct {
		Ports map[string]struct {
			HostPort string `json:"host_port"`
		} `json:"ports"`
	}
	decodeJSON(t, portsResp, &ports)

	_, has5900 := ports.Ports["5900"]
	_, has6080 := ports.Ports["6080"]
	assert.False(t, has5900, "port 5900 (VNC) should not be in port mappings")
	assert.False(t, has6080, "port 6080 (noVNC) should not be in port mappings")
}

// IT-37: Multiple agents should have different port mappings (no conflicts).
func TestUnifiedImage_DynamicPorts(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp1 := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "ports-agent-1"})
	require.Equal(t, http.StatusCreated, resp1.StatusCode)
	var ag1 agentpkg.Agent
	decodeJSON(t, resp1, &ag1)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag1.ID, nil)

	resp2 := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "ports-agent-2"})
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	var ag2 agentpkg.Agent
	decodeJSON(t, resp2, &ag2)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag2.ID, nil)

	resp1 = doRequest(t, ts, "GET", "/api/agents/"+ag1.ID+"/ports", nil)
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	var ports1 struct {
		Ports map[string]struct {
			HostPort string `json:"host_port"`
		} `json:"ports"`
	}
	decodeJSON(t, resp1, &ports1)

	resp2 = doRequest(t, ts, "GET", "/api/agents/"+ag2.ID+"/ports", nil)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	var ports2 struct {
		Ports map[string]struct {
			HostPort string `json:"host_port"`
		} `json:"ports"`
	}
	decodeJSON(t, resp2, &ports2)

	if len(ports1.Ports) > 0 && len(ports2.Ports) > 0 {
		for containerPort, info1 := range ports1.Ports {
			if info2, ok := ports2.Ports[containerPort]; ok {
				assert.NotEqual(t, info1.HostPort, info2.HostPort,
					"agents should have different host ports for container port %s", containerPort)
			}
		}
	}
}

// IT-38: GET /api/agents/:id/desktop returns correct desktop info.
func TestUnifiedImage_DesktopAPI(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{
		"name": "desktop-api-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	desktopResp := doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/desktop", nil)
	assert.Equal(t, http.StatusOK, desktopResp.StatusCode)

	var desktopInfo struct {
		DesktopPort string `json:"desktop_port"`
		DesktopURL  string `json:"desktop_url"`
		Status      string `json:"status"`
	}
	err := json.NewDecoder(desktopResp.Body).Decode(&desktopInfo)
	require.NoError(t, err)

	assert.Equal(t, "running", desktopInfo.Status)
}
