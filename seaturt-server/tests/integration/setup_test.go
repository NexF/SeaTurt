//go:build integration

package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/store"
)

const testImage = "seaturt/sandbox:test"

var (
	dockerMgr     *container.Manager
	testDBPath    string
	testWorkspace string
	testStore     *store.Store
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// 1. Init Docker manager
	var err error
	dockerMgr, err = container.NewManager("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: Docker not available: %v\n", err)
		os.Exit(0)
	}
	defer dockerMgr.Close()

	// 2. Check test image exists
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	exists, err := dockerMgr.ImageExists(ctx, testImage)
	cancel()
	if err != nil || !exists {
		fmt.Fprintf(os.Stderr, "SKIP: Test image %q not found. Build with: make build-test-image\n", testImage)
		os.Exit(0)
	}

	// 3. Create temp workspace root
	testWorkspace, err = os.MkdirTemp("", "seaturt-test-ws-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: create temp workspace: %v\n", err)
		os.Exit(1)
	}

	// 4. Create temp SQLite
	tmpDB, err := os.CreateTemp("", "seaturt-test-*.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: create temp db: %v\n", err)
		os.Exit(1)
	}
	testDBPath = tmpDB.Name()
	tmpDB.Close()

	testStore, err = store.New(testDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: init test store: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	// 5. Cleanup
	testStore.Close()
	os.Remove(testDBPath)
	os.RemoveAll(testWorkspace)
	cleanupTestContainers()

	os.Exit(code)
}

// createTestContainer creates a running test container with a temp workspace.
// It registers cleanup via t.Cleanup.
func createTestContainer(t *testing.T) (containerID string, workspacePath string) {
	t.Helper()
	ctx := context.Background()

	workspacePath, err := os.MkdirTemp(testWorkspace, "agent-*")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	id, err := dockerMgr.CreateContainer(ctx, container.CreateContainerOpts{
		AgentID:       fmt.Sprintf("test_%d", time.Now().UnixNano()),
		Image:         testImage,
		WorkspacePath: workspacePath,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	if err := dockerMgr.StartContainer(ctx, id); err != nil {
		dockerMgr.RemoveContainer(ctx, id)
		t.Fatalf("start container: %v", err)
	}

	// Give container a moment to initialize
	time.Sleep(500 * time.Millisecond)

	t.Cleanup(func() {
		ctx := context.Background()
		_ = dockerMgr.StopContainer(ctx, id)
		_ = dockerMgr.RemoveContainer(ctx, id)
		os.RemoveAll(workspacePath)
	})

	return id, workspacePath
}

// cleanupTestContainers removes any leftover test containers.
func cleanupTestContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containers, err := dockerMgr.ListContainers(ctx)
	if err != nil {
		return
	}
	for _, c := range containers {
		_ = dockerMgr.RemoveContainer(ctx, c.ID)
	}
}
