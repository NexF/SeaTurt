//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/store"
)

var (
	testCfg       *config.Config
	dockerMgr     *container.Manager
	llmClient     *llm.Client
	testStore     *store.Store
	testDBPath    string
	testWorkspace string
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// 1. Load real config (from config.yaml or env vars)
	testCfg = config.Load()

	// 2. Verify LLM provider is configured
	endpoint, err := testCfg.ResolveLLM("", "")
	if err != nil || endpoint.BaseURL == "" {
		fmt.Fprintf(os.Stderr, "SKIP: No LLM provider configured. Set config.yaml or LLM_BASE_URL env.\n")
		os.Exit(0)
	}

	llmClient = llm.NewClient(
		endpoint.BaseURL,
		endpoint.APIKey,
		endpoint.Model,
		endpoint.API,
		endpoint.Headers,
		endpoint.Input,
	)

	// 3. Init Docker manager
	dockerMgr, err = container.NewManager(testCfg.DockerHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: Docker not available: %v\n", err)
		os.Exit(0)
	}
	defer dockerMgr.Close()

	// 4. Check sandbox image exists
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	exists, err := dockerMgr.ImageExists(ctx, testCfg.SandboxImage)
	cancel()
	if err != nil || !exists {
		// Fall back to test image
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		exists2, _ := dockerMgr.ImageExists(ctx2, "seaturt/sandbox:test")
		cancel2()
		if !exists2 {
			fmt.Fprintf(os.Stderr, "SKIP: No sandbox image found (%s or seaturt/sandbox:test)\n", testCfg.SandboxImage)
			os.Exit(0)
		}
		testCfg.SandboxImage = "seaturt/sandbox:test"
	}

	// 5. Create temp workspace and DB
	testWorkspace, err = os.MkdirTemp("", "seaturt-e2e-ws-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: create temp workspace: %v\n", err)
		os.Exit(1)
	}

	tmpDB, err := os.CreateTemp("", "seaturt-e2e-*.db")
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

	// Cleanup
	testStore.Close()
	os.Remove(testDBPath)
	os.RemoveAll(testWorkspace)
	cleanupE2EContainers()

	os.Exit(code)
}

func cleanupE2EContainers() {
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
