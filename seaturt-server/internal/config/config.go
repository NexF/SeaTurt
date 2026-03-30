package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	// 服务配置
	ServerPort int    `yaml:"server_port" json:"server_port"`
	LogLevel   string `yaml:"log_level" json:"log_level"`

	// Docker 配置
	DockerHost    string `yaml:"docker_host" json:"docker_host"`
	SandboxImage  string `yaml:"sandbox_image" json:"sandbox_image"`
	WorkspaceRoot string `yaml:"workspace_root" json:"workspace_root"`

	// LLM 配置 — 多 Provider 多 Model
	Providers       map[string]*ProviderConfig `yaml:"providers" json:"providers"`
	DefaultProvider string                     `yaml:"default_provider" json:"default_provider"`
	DefaultModel    string                     `yaml:"default_model" json:"default_model"`

	// 数据库配置
	DBPath string `yaml:"db_path" json:"db_path"`

	// Agent 默认配置
	DefaultMCPServers []MCPServerConfig `yaml:"default_mcp_servers" json:"default_mcp_servers"`
	CommandTimeout    int               `yaml:"command_timeout" json:"command_timeout"`

	// 多模态配置
	MaxImageSize int `yaml:"max_image_size" json:"max_image_size"` // bytes, default 20MB

	// 容器配置
	Container ContainerConfig `yaml:"container" json:"container"`

	// MCP Bins 目录（宿主机路径，存放预编译的 MCP Server 二进制/脚本）
	// 默认为可执行文件所在目录下的 mcp-bins/
	MCPBinsDir string `yaml:"mcp_bins_dir" json:"mcp_bins_dir"`

	// Prompts 目录（宿主机路径，存放 system prompt 模板文件）
	// 默认为可执行文件所在目录下的 prompts/
	PromptsDir string `yaml:"prompts_dir" json:"prompts_dir"`

	// MCP Servers 源码目录（宿主机路径，存放 MCP Server Python/JS 源码）
	// 默认为可执行文件所在目录下的 mcp-servers/
	// 这些源码在 Agent 创建时会被复制到 workspace/.seaturt/mcp-servers/
	MCPServersDir string `yaml:"mcp_servers_dir" json:"mcp_servers_dir"`
}

// ContainerConfig holds container-level configuration.
type ContainerConfig struct {
	ShmSize int64 `yaml:"shm_size" json:"shm_size"` // /dev/shm size in bytes, default 2GB
}

// ProviderConfig describes an LLM provider (OpenAI-compatible).
type ProviderConfig struct {
	BaseURL string        `yaml:"base_url" json:"baseUrl"`
	API     string        `yaml:"api" json:"api"`
	APIKey  string        `yaml:"api_key" json:"apiKey"`
	Models  []ModelConfig `yaml:"models" json:"models"`
}

// ModelConfig describes a model within a provider.
type ModelConfig struct {
	ID            string            `yaml:"id" json:"id"`
	Name          string            `yaml:"name" json:"name"`
	Reasoning     bool              `yaml:"reasoning" json:"reasoning"`
	Input         []string          `yaml:"input" json:"input"`
	ContextWindow int               `yaml:"context_window" json:"contextWindow"`
	MaxTokens     int               `yaml:"max_tokens" json:"maxTokens"`
	Headers       map[string]string `yaml:"headers" json:"headers"`
	Cost          *CostConfig       `yaml:"cost,omitempty" json:"cost,omitempty"`
}

// CostConfig tracks per-token pricing.
type CostConfig struct {
	Input      float64 `yaml:"input" json:"input"`
	Output     float64 `yaml:"output" json:"output"`
	CacheRead  float64 `yaml:"cache_read" json:"cacheRead"`
	CacheWrite float64 `yaml:"cache_write" json:"cacheWrite"`
}

type MCPServerConfig struct {
	Name    string `yaml:"name" json:"name"`
	Command string `yaml:"command" json:"command"`
}

// LLMEndpoint holds the resolved LLM connection info for a specific provider+model.
type LLMEndpoint struct {
	BaseURL   string
	APIKey    string
	Model     string
	API       string            // "openai-completions", "anthropic-messages", etc.
	Input     []string          // supported input types, e.g. ["text", "image"]
	Headers   map[string]string // custom headers (per-model)
	Reasoning bool              // true if model is a reasoning model (e.g. DeepSeek R1)
}

// Load loads config from YAML file (if exists) with environment variable overrides.
// Search order: $CONFIG_PATH → ./config.yaml → ~/.seaturt/config.yaml
func Load() *Config {
	cfg := defaults()

	// Try to load YAML
	if yamlPath := findConfigFile(); yamlPath != "" {
		if data, err := os.ReadFile(yamlPath); err == nil {
			_ = yaml.Unmarshal(data, cfg)
		}
	}

	// Env overrides (higher priority than YAML)
	applyEnvOverrides(cfg)

	// Expand ~ in paths (YAML values may contain ~)
	cfg.WorkspaceRoot = expandHome(cfg.WorkspaceRoot)
	cfg.DBPath = expandHome(cfg.DBPath)

	// Backward compat: if no providers defined but LLM_BASE_URL env is set,
	// create a "default" provider from env vars.
	if len(cfg.Providers) == 0 {
		baseURL := getEnv("LLM_BASE_URL", "")
		apiKey := getEnv("LLM_API_KEY", "")
		model := getEnv("LLM_MODEL", "")
		if baseURL != "" {
			if model == "" {
				model = "claude-sonnet-4-20250514"
			}
			cfg.Providers = map[string]*ProviderConfig{
				"default": {
					BaseURL: baseURL,
					API:     "openai-completions",
					APIKey:  apiKey,
					Models: []ModelConfig{
						{ID: model, Name: model},
					},
				},
			}
			cfg.DefaultProvider = "default"
			cfg.DefaultModel = model
		}
	}

	return cfg
}

// ResolveLLM returns the LLMEndpoint for a given provider+model.
// If provider is empty, uses DefaultProvider. If model is empty, uses DefaultModel.
func (c *Config) ResolveLLM(provider, model string) (*LLMEndpoint, error) {
	if provider == "" {
		provider = c.DefaultProvider
	}
	if model == "" {
		model = c.DefaultModel
	}

	p, ok := c.Providers[provider]
	if !ok {
		return nil, fmt.Errorf("unknown LLM provider: %q", provider)
	}

	// Find model config
	var mc *ModelConfig
	for i := range p.Models {
		if p.Models[i].ID == model || p.Models[i].Name == model {
			mc = &p.Models[i]
			break
		}
	}
	// If model not found, still allow — use provider base info with the model id as-is
	endpoint := &LLMEndpoint{
		BaseURL: p.BaseURL,
		APIKey:  p.APIKey,
		Model:   model,
		API:     p.API,
	}
	if mc != nil {
		endpoint.Model = mc.ID
		endpoint.Headers = mc.Headers
		endpoint.Input = mc.Input
		endpoint.Reasoning = mc.Reasoning
	}
	return endpoint, nil
}

// defaults returns a Config with sane defaults.
func defaults() *Config {
	return &Config{
		ServerPort:    8080,
		LogLevel:      "info",
		DockerHost:    "unix:///var/run/docker.sock",
		SandboxImage:  "seaturt/sandbox:latest",
		WorkspaceRoot: expandHome("~/.seaturt/workspaces"),
		DBPath:        expandHome("~/.seaturt/data.db"),
		Providers:     make(map[string]*ProviderConfig),
		DefaultMCPServers: []MCPServerConfig{
			{Name: "core", Command: "mcp-server-core"},
		},
		CommandTimeout: 300,
		MaxImageSize:   20 * 1024 * 1024, // 20MB
		Container: ContainerConfig{
			ShmSize: 2 * 1024 * 1024 * 1024, // 2GB
		},
	}
}

// applyEnvOverrides applies environment variable overrides.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("SERVER_PORT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.ServerPort = i
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("DOCKER_HOST"); v != "" {
		cfg.DockerHost = v
	}
	if v := os.Getenv("SANDBOX_IMAGE"); v != "" {
		cfg.SandboxImage = v
	}
	if v := os.Getenv("WORKSPACE_ROOT"); v != "" {
		cfg.WorkspaceRoot = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("COMMAND_TIMEOUT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.CommandTimeout = i
		}
	}
	if v := os.Getenv("DEFAULT_PROVIDER"); v != "" {
		cfg.DefaultProvider = v
	}
	if v := os.Getenv("DEFAULT_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv("MAX_IMAGE_SIZE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxImageSize = i
		}
	}
}

// findConfigFile searches for config.yaml in standard locations.
func findConfigFile() string {
	// 1. Env-specified path
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 2. Current directory
	if _, err := os.Stat("config.yaml"); err == nil {
		abs, _ := filepath.Abs("config.yaml")
		return abs
	}
	// 3. Home dir
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".seaturt", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ServerDir returns the directory where the seaturt-server binary resides.
// This is used to locate mcp-bins/ and other server-relative paths.
func (c *Config) ServerDir() string {
	if c.MCPBinsDir != "" {
		return filepath.Dir(c.MCPBinsDir)
	}
	exe, err := os.Executable()
	if err != nil {
		// Fallback to working directory
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

// GetMCPBinsDir returns the path to the MCP binaries directory.
func (c *Config) GetMCPBinsDir() string {
	if c.MCPBinsDir != "" {
		return expandHome(c.MCPBinsDir)
	}
	return filepath.Join(c.ServerDir(), "mcp-bins")
}

// GetPromptsDir returns the path to the prompts template directory.
// Default: <serverDir>/prompts/
func (c *Config) GetPromptsDir() string {
	if c.PromptsDir != "" {
		return expandHome(c.PromptsDir)
	}
	return filepath.Join(c.ServerDir(), "prompts")
}

// GetMCPServersSourceDir returns the path to the MCP servers source directory.
// This directory contains Python/JS source code that gets deployed to each
// agent's workspace at creation time.
// Default: <serverDir>/mcp-servers/
func (c *Config) GetMCPServersSourceDir() string {
	if c.MCPServersDir != "" {
		return expandHome(c.MCPServersDir)
	}
	return filepath.Join(c.ServerDir(), "mcp-servers")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func expandHome(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}
