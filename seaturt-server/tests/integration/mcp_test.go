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

// IT-03: MCP Client connection — docker exec starts MCP Server, completes initialize handshake
func TestMCPClientConnect(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	client, err := mcp.NewClient(ctx, dockerMgr, mcp.ClientConfig{
		Name:        "core",
		ContainerID: containerID,
		Command:     "mcp-server-core",
	})
	require.NoError(t, err)
	defer client.Close()

	assert.Equal(t, "core", client.Name())
	assert.NotEmpty(t, client.Tools(), "should have cached tools after initialization")
}

// IT-04: MCP tools/list — returns expected tool list
func TestMCPToolsList(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	client, err := mcp.NewClient(ctx, dockerMgr, mcp.ClientConfig{
		Name:        "core",
		ContainerID: containerID,
		Command:     "mcp-server-core",
	})
	require.NoError(t, err)
	defer client.Close()

	tools := client.Tools()
	require.NotEmpty(t, tools)

	// Verify expected tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"shell_exec", "file_read", "file_write", "file_list"}
	for _, name := range expectedTools {
		assert.True(t, toolNames[name], "expected tool %q not found", name)
	}
}

// IT-05: MCP shell_exec — execute 'echo hello' and verify result
func TestMCPShellExec(t *testing.T) {
	t.Parallel()
	containerID, _ := createTestContainer(t)

	ctx := context.Background()
	client, err := mcp.NewClient(ctx, dockerMgr, mcp.ClientConfig{
		Name:        "core",
		ContainerID: containerID,
		Command:     "mcp-server-core",
	})
	require.NoError(t, err)
	defer client.Close()

	result, err := client.CallTool("shell_exec", map[string]any{
		"command": "echo hello",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	assert.Contains(t, result.Content[0].Text, "hello")
}

// IT-06: MCP file_write + file_read — write via MCP, read back, content matches
func TestMCPFileReadWrite(t *testing.T) {
	t.Parallel()
	containerID, wsPath := createTestContainer(t)

	ctx := context.Background()
	client, err := mcp.NewClient(ctx, dockerMgr, mcp.ClientConfig{
		Name:        "core",
		ContainerID: containerID,
		Command:     "mcp-server-core",
	})
	require.NoError(t, err)
	defer client.Close()

	testContent := "integration test content\nline 2"
	testPath := "/workspace/test_mcp_rw.txt"

	// Write file via MCP
	writeResult, err := client.CallTool("file_write", map[string]any{
		"path":    testPath,
		"content": testContent,
	})
	require.NoError(t, err)
	require.NotNil(t, writeResult)
	assert.False(t, writeResult.IsError)

	// Give filesystem sync time
	time.Sleep(200 * time.Millisecond)

	// Read file via MCP
	readResult, err := client.CallTool("file_read", map[string]any{
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
