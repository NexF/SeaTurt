package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/api"
	"github.com/seaturt/server/internal/builtin"
	"github.com/seaturt/server/internal/config"
	"github.com/seaturt/server/internal/container"
	cronpkg "github.com/seaturt/server/internal/cron"
	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/store"
)

// webDist embeds the frontend build output.
// When building with `make release`, the web/dist directory is populated first.
// In development mode, this will be empty and webFS will be nil.
//
//go:embed web/dist/*
var webDist embed.FS

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
	llmClient := llm.NewClient(llmEndpoint.BaseURL, llmEndpoint.APIKey, llmEndpoint.Model, llmEndpoint.API, llmEndpoint.Headers, llmEndpoint.Input)
	slog.Info("LLM client initialized",
		"base_url", llmEndpoint.BaseURL,
		"model", llmEndpoint.Model,
		"api", llmEndpoint.API,
	)

	// 初始化 Agent Manager
	agentMgr := agent.NewManager(cfg, db, dockerMgr, llmClient)

	// 初始化 Cron Scheduler (agentMgr implements cron.AgentExecutor)
	scheduler := cronpkg.NewScheduler(db, agentMgr)

	// 初始化 Builtin Tools Router (cron management tools)
	cronHandlers := builtin.NewCronHandlers(db, scheduler)
	builtinRouter := builtin.NewRouter(cronHandlers)
	agentMgr.SetBuiltinRouter(builtinRouter)

	// 启动时同步 Agent 状态与 Docker 容器实际状态
	agentMgr.SyncAgentStates(context.Background())

	// 加载已启用的定时任务并启动调度器
	if err := scheduler.LoadAll(); err != nil {
		slog.Warn("failed to load cron jobs", "err", err)
	}
	scheduler.Start()
	defer scheduler.Stop()

	// Check if embedded frontend exists (production mode)
	var webFS fs.FS
	if sub, err := fs.Sub(webDist, "web/dist"); err == nil {
		// Verify it has content (index.html)
		if f, err := sub.Open("index.html"); err == nil {
			f.Close()
			webFS = sub
			slog.Info("embedded frontend detected, serving in production mode")
		}
	}

	// 初始化 HTTP Server
	server := api.NewServer(cfg.ServerPort, agentMgr, cfg.MaxImageSize, webFS, scheduler)

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
