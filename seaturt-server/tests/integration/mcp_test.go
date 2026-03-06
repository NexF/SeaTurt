//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seaturt/server/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-03: MCP Executor — docker exec starts MCP Server, completes initialize handshake + tool call
func TestMCPExecutorShellExec(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")
	executor := mcp.NewExecutor(dockerMgr, containerID, containerToolsDir)

	result, err := executor.Execute(ctx, "mcp-server-core", "shell_exec", map[string]any{
		"command": "echo hello",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	assert.Contains(t, result.Content[0].Text, "hello")

	_ = wsPath
}

// IT-04: MCP Router — AllTools returns tool list from YAML registry
func TestMCPRouterToolsList(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)
	tools := router.AllTools()
	require.NotEmpty(t, tools)

	// Verify expected tool names (qualified with server prefix)
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"core-shell_exec", "core-file_read", "core-file_write", "core-file_list"}
	for _, name := range expectedTools {
		assert.True(t, toolNames[name], "expected tool %q not found", name)
	}
}

// IT-05: MCP Router — Route shell_exec through executor
func TestMCPRouterShellExec(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)

	ctx := context.Background()
	result, err := router.Route(ctx, "core-shell_exec", map[string]any{
		"command": "echo hello_from_router",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	assert.Contains(t, result.Content[0].Text, "hello_from_router")
}

// IT-06: MCP file_write + file_read — write via Router, read back, content matches
func TestMCPFileReadWrite(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	router := createTestRouter(t, containerID, wsPath)
	ctx := context.Background()

	testContent := "integration test content\nline 2"
	testPath := "/workspace/test_mcp_rw.txt"

	// Write file via MCP
	writeResult, err := router.Route(ctx, "core-file_write", map[string]any{
		"path":    testPath,
		"content": testContent,
	})
	require.NoError(t, err)
	require.NotNil(t, writeResult)
	assert.False(t, writeResult.IsError)

	// Give filesystem sync time
	time.Sleep(200 * time.Millisecond)

	// Read file via MCP
	readResult, err := router.Route(ctx, "core-file_read", map[string]any{
		"path": testPath,
	})
	require.NoError(t, err)
	require.NotNil(t, readResult)
	assert.False(t, readResult.IsError)
	require.NotEmpty(t, readResult.Content)
	assert.Equal(t, testContent, readResult.Content[0].Text)

	// Also verify on host filesystem
	hostPath := filepath.Join(wsPath, "test_mcp_rw.txt")
	data, err := os.ReadFile(hostPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(data))
}

// IT-06b: MCP Executor — binary path resolution works correctly
// This test explicitly verifies the fix: bin files in /workspace/.seaturt/tools/
// are found by the Executor's filepath.Join(toolsDir, command) logic.
func TestMCPExecutorBinaryPathResolution(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	containerToolsDir := filepath.Join("/workspace", ".seaturt", "tools")

	// Verify the binary actually exists at the expected path
	verifyCmd := []string{"test", "-x", filepath.Join(containerToolsDir, "mcp-server-core")}
	verifyResult, err := dockerMgr.Exec(ctx, containerID, verifyCmd)
	require.NoError(t, err, "binary should be executable at expected path")
	assert.Equal(t, 0, verifyResult.ExitCode, "mcp-server-core should exist and be executable in %s", containerToolsDir)

	// Now verify the Executor can actually use it
	executor := mcp.NewExecutor(dockerMgr, containerID, containerToolsDir)
	result, err := executor.Execute(ctx, "mcp-server-core", "shell_exec", map[string]any{
		"command": "echo path_test_ok",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "path_test_ok")
}
