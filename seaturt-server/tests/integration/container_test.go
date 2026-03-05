//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seaturt/server/internal/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-01: Container lifecycle — create → start → stop → start → delete
func TestContainerLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	wsPath, err := os.MkdirTemp(testWorkspace, "lifecycle-*")
	require.NoError(t, err)
	defer os.RemoveAll(wsPath)

	// Create
	containerID, err := dockerMgr.CreateContainer(ctx, container.CreateContainerOpts{
		AgentID:       fmt.Sprintf("test_lifecycle_%d", time.Now().UnixNano()),
		Image:         testImage,
		WorkspacePath: wsPath,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)

	defer func() {
		_ = dockerMgr.RemoveContainer(ctx, containerID)
	}()

	// Inspect — should be "created"
	status, err := dockerMgr.InspectContainer(ctx, containerID)
	require.NoError(t, err)
	assert.False(t, status.Running)
	assert.Equal(t, "created", status.Status)

	// Start
	err = dockerMgr.StartContainer(ctx, containerID)
	require.NoError(t, err)

	status, err = dockerMgr.InspectContainer(ctx, containerID)
	require.NoError(t, err)
	assert.True(t, status.Running)
	assert.Equal(t, "running", status.Status)

	// Stop
	err = dockerMgr.StopContainer(ctx, containerID)
	require.NoError(t, err)

	status, err = dockerMgr.InspectContainer(ctx, containerID)
	require.NoError(t, err)
	assert.False(t, status.Running)
	assert.Equal(t, "exited", status.Status)

	// Restart (resume)
	err = dockerMgr.StartContainer(ctx, containerID)
	require.NoError(t, err)

	status, err = dockerMgr.InspectContainer(ctx, containerID)
	require.NoError(t, err)
	assert.True(t, status.Running)

	// Delete (force remove while running)
	err = dockerMgr.RemoveContainer(ctx, containerID)
	require.NoError(t, err)
}

// IT-02: Workspace mount — host writes → container reads; container writes → host reads
func TestWorkspaceMount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	containerID, wsPath := createTestContainer(t)

	// 1. Host writes a file → container should see it
	testContent := "hello from host"
	hostFile := filepath.Join(wsPath, "host_file.txt")
	err := os.WriteFile(hostFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Give filesystem sync a moment
	time.Sleep(200 * time.Millisecond)

	result, err := dockerMgr.Exec(ctx, containerID, []string{"cat", "/workspace/host_file.txt"})
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, testContent, result.Stdout)

	// 2. Container writes a file → host should see it
	_, err = dockerMgr.Exec(ctx, containerID, []string{
		"sh", "-c", "echo 'hello from container' > /workspace/container_file.txt",
	})
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	containerFile := filepath.Join(wsPath, "container_file.txt")
	data, err := os.ReadFile(containerFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello from container")
}
