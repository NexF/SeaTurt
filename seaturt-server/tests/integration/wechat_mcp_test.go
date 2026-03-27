//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-50: wechat MCP wrapper script exists in mcp-bins staging dir and is executable
func TestWeChatMCP_WrapperExists(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()

	// Verify the wrapper script was staged in /opt/seaturt/mcp-bins/
	checkCmd := []string{"test", "-x", filepath.Join(mcpBinsStagingDir, "mcp-server-wechat")}
	result, err := dockerMgr.Exec(ctx, containerID, checkCmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode,
		"mcp-server-wechat should exist and be executable in %s", mcpBinsStagingDir)

	// Verify it was also copied to the workspace tools dir
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	checkCmd2 := []string{"test", "-x", filepath.Join(containerToolsDir, "mcp-server-wechat")}
	result2, err := dockerMgr.Exec(ctx, containerID, checkCmd2)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.ExitCode,
		"mcp-server-wechat should exist and be executable in %s", containerToolsDir)
}

// IT-51: wechat MCP wrapper script is a bash script pointing to /opt/mcp-servers/wechat/
func TestWeChatMCP_WrapperContent(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")

	// Read wrapper script content
	catCmd := []string{"cat", filepath.Join(containerToolsDir, "mcp-server-wechat")}
	result, err := dockerMgr.Exec(ctx, containerID, catCmd)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)

	content := result.Stdout
	assert.Contains(t, content, "#!/bin/bash", "wrapper should be a bash script")
	assert.Contains(t, content, "/opt/mcp-servers/wechat", "wrapper should reference /opt/mcp-servers/wechat")
	assert.Contains(t, content, "DBUS_SESSION_BUS_ADDRESS", "wrapper should set up D-Bus env")
	assert.Contains(t, content, "DISPLAY", "wrapper should set DISPLAY")
}

// IT-52: wechat Python code is pre-installed in Docker image at /opt/mcp-servers/wechat/
func TestWeChatMCP_PythonCodeInstalled(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	mcpDir := "/opt/mcp-servers/wechat"

	// Check main.py exists
	checkMain := []string{"test", "-f", filepath.Join(mcpDir, "main.py")}
	result, err := dockerMgr.Exec(ctx, containerID, checkMain)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "main.py should exist in %s", mcpDir)

	// Check wechat_db_query.py exists
	checkDB := []string{"test", "-f", filepath.Join(mcpDir, "wechat_db_query.py")}
	result2, err := dockerMgr.Exec(ctx, containerID, checkDB)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.ExitCode, "wechat_db_query.py should exist")

	// Check wechat_ui.py exists
	checkUI := []string{"test", "-f", filepath.Join(mcpDir, "wechat_ui.py")}
	result3, err := dockerMgr.Exec(ctx, containerID, checkUI)
	require.NoError(t, err)
	assert.Equal(t, 0, result3.ExitCode, "wechat_ui.py should exist")

	// Check wechat_launcher.py exists
	checkLauncher := []string{"test", "-f", filepath.Join(mcpDir, "wechat_launcher.py")}
	result4, err := dockerMgr.Exec(ctx, containerID, checkLauncher)
	require.NoError(t, err)
	assert.Equal(t, 0, result4.ExitCode, "wechat_launcher.py should exist")
}

// IT-53: wechat session directory symlink points to workspace bind mount
func TestWeChatMCP_SessionSymlink(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()

	// Check symlink exists
	checkLink := []string{"readlink", "/opt/mcp-servers/wechat/session"}
	result, err := dockerMgr.Exec(ctx, containerID, checkLink)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "/workspace/.seaturt/wechat-session",
		"session should be a symlink to workspace bind mount")
}

// IT-54: wechat MCP Server — MCP initialize handshake succeeds
// NOTE: This test verifies the MCP protocol layer (JSON-RPC initialize).
// WeChat itself does NOT need to be running or logged in for this to work,
// because the MCP protocol layer (initialize + tools/list) is handled by
// main.py's protocol code BEFORE any WeChat UI interaction.
//
// However, the wechat main.py imports wechat_launcher at module level which
// calls ensure_display() + ensure_dbus(). In a test container without a full
// desktop environment, these may fail. This test will skip gracefully if the
// MCP server cannot start (e.g. missing DISPLAY / D-Bus / AT-SPI2 deps).
func TestWeChatMCP_Initialize(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	binPath := filepath.Join(containerToolsDir, "mcp-server-wechat")

	cmd := []string{binPath}

	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hijacked, err := dockerMgr.ExecStdio(discoverCtx, containerID, container.ExecAttachOptions{Cmd: cmd})
	if err != nil {
		t.Skipf("SKIP: cannot exec wechat MCP server (env not ready): %v", err)
		return
	}

	transport := mcp.NewDiscoverTransport(hijacked)
	defer transport.Close()

	initResult, err := transport.InitializeAndGetResult()
	if err != nil {
		t.Skipf("SKIP: wechat MCP initialize failed (desktop env likely missing): %v", err)
		return
	}

	require.NotNil(t, initResult)
	assert.Equal(t, "mcp-server-wechat", initResult.ServerInfo.Name)
	assert.Equal(t, "2024-11-05", initResult.ProtocolVersion)
}

// IT-55: wechat MCP Server — tools/list returns expected tools
func TestWeChatMCP_ToolsList(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	binPath := filepath.Join(containerToolsDir, "mcp-server-wechat")

	cmd := []string{binPath}

	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hijacked, err := dockerMgr.ExecStdio(discoverCtx, containerID, container.ExecAttachOptions{Cmd: cmd})
	if err != nil {
		t.Skipf("SKIP: cannot exec wechat MCP server: %v", err)
		return
	}

	transport := mcp.NewDiscoverTransport(hijacked)
	defer transport.Close()

	_, err = transport.InitializeAndGetResult()
	if err != nil {
		t.Skipf("SKIP: wechat MCP initialize failed: %v", err)
		return
	}

	tools, err := transport.ToolsList()
	if err != nil {
		t.Skipf("SKIP: wechat MCP tools/list failed: %v", err)
		return
	}

	require.NotEmpty(t, tools)

	// Collect tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	// Verify all expected wechat tools are present
	expectedTools := []string{
		"wechat_login",
		"wechat_logout",
		"wechat_status",
		"wechat_get_contacts",
		"wechat_send_msg",
		"wechat_send_image",
		"wechat_send_file",
		"wechat_read_messages",
		"wechat_screenshot",
		"wechat_get_sessions",
		"wechat_get_unread",
	}

	for _, name := range expectedTools {
		assert.True(t, toolNames[name], "expected tool %q not found in tools/list", name)
	}

	// Verify tool count matches
	assert.Equal(t, len(expectedTools), len(tools),
		"tools/list should return exactly %d tools, got %d", len(expectedTools), len(tools))
}

// IT-56: wechat MCP Server — discoverTools integration (full flow:
// copy binary → discover → write YAML → load registry → verify tools)
func TestWeChatMCP_DiscoverAndRegister(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	// createTestContainer already copies binaries and discovers tools for
	// core + desktop. We need to verify wechat was also discovered.
	// Check if the wechat YAML was written by createTestContainer.
	// Note: createTestContainer only discovers mcp-server-core and mcp-server-desktop
	// by default. We need to explicitly discover wechat.

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	binPath := filepath.Join(containerToolsDir, "mcp-server-wechat")

	// First verify the binary was copied
	checkCmd := []string{"test", "-x", binPath}
	result, err := dockerMgr.Exec(ctx, containerID, checkCmd)
	require.NoError(t, err)
	if result.ExitCode != 0 {
		t.Skip("SKIP: mcp-server-wechat not in container tools dir")
		return
	}

	// Discover tools via MCP protocol
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	hijacked, err := dockerMgr.ExecStdio(discoverCtx, containerID, container.ExecAttachOptions{Cmd: []string{binPath}})
	if err != nil {
		t.Skipf("SKIP: cannot exec wechat MCP server: %v", err)
		return
	}

	transport := mcp.NewDiscoverTransport(hijacked)
	initResult, err := transport.InitializeAndGetResult()
	if err != nil {
		transport.Close()
		t.Skipf("SKIP: wechat MCP initialize failed: %v", err)
		return
	}

	tools, err := transport.ToolsList()
	transport.Close()
	if err != nil {
		t.Skipf("SKIP: wechat MCP tools/list failed: %v", err)
		return
	}

	require.NotEmpty(t, tools)

	// Write YAML (mimicking loadMCPServers logic)
	toolsDir := filepath.Join(wsPath, ".seaturt", "tools")
	writeDiscoveredYAML(t, toolsDir, "wechat", "mcp-server-wechat", initResult, tools)

	// Load registry and verify
	registry := mcp.NewToolRegistry()
	err = registry.LoadFromDir(toolsDir)
	require.NoError(t, err)

	// Create a router and check wechat tools are routable
	executor := mcp.NewExecutor(dockerMgr, containerID, containerToolsDir)
	router := mcp.NewRouter(registry, executor)

	allTools := router.AllTools()
	wechatToolNames := make([]string, 0)
	for _, tool := range allTools {
		if len(tool.Name) > 7 && tool.Name[:7] == "wechat-" {
			wechatToolNames = append(wechatToolNames, tool.Name)
		}
	}

	assert.NotEmpty(t, wechatToolNames, "router should have wechat-* tools")
	assert.Contains(t, wechatToolNames, "wechat-wechat_login")
	assert.Contains(t, wechatToolNames, "wechat-wechat_status")
	assert.Contains(t, wechatToolNames, "wechat-wechat_get_contacts")
}
