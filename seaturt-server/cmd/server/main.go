package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/api"
	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/container"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/store"
)

func main() {
	cfg := config.Load()

	// 初始化结构化日志
	initLogger(cfg.LogLevel)

	slog.Info("starting SeaTurt server",
		"port", cfg.ServerPort,
		"workspace", cfg.WorkspaceRoot,
		"db", cfg.DBPath,
	)

	// 确保必要目录存在
	if err := ensureDirs(cfg); err != nil {
		slog.Error("failed to create directories", "err", err)
		os.Exit(1)
	}

	// 初始化数据库
	db, err := store.New(cfg.DBPath)
	if err != nil {
		slog.Error("failed to initialize database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// 初始化 Docker 管理器
	dockerMgr, err := container.NewManager(cfg.DockerHost)
	if err != nil {
		slog.Error("failed to initialize docker manager", "err", err)
		os.Exit(1)
	}
	defer dockerMgr.Close()

	// 初始化 LLM Client
	llmEndpoint, err := cfg.ResolveLLM("", "")
	if err != nil {
		slog.Error("failed to resolve LLM config", "err", err)
		os.Exit(1)
	}
	llmClient := llm.NewClient(llmEndpoint.BaseURL, llmEndpoint.APIKey, llmEndpoint.Model, llmEndpoint.API, llmEndpoint.Headers)
	slog.Info("LLM client initialized",
		"base_url", llmEndpoint.BaseURL,
		"model", llmEndpoint.Model,
		"api", llmEndpoint.API,
	)

	// 初始化 Agent Manager
	agentMgr := agent.NewManager(cfg, db, dockerMgr, llmClient)

	// 启动时同步 Agent 状态与 Docker 容器实际状态
	agentMgr.SyncAgentStates(context.Background())

	// 初始化 HTTP Server
	server := api.NewServer(cfg.ServerPort, agentMgr, cfg.MaxImageSize)

	fmt.Printf("SeaTurt server listening on :%d\n", cfg.ServerPort)
	if err := server.Run(); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func initLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}

func ensureDirs(cfg *config.Config) error {
	dirs := []string{
		cfg.WorkspaceRoot,
		filepath.Dir(cfg.DBPath),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}
