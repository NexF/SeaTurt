package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"tilde path", "~/.seaturt/workspaces", home + "/.seaturt/workspaces"},
		{"absolute path", "/tmp/workspaces", "/tmp/workspaces"},
		{"relative path", "data/workspaces", "data/workspaces"},
		{"empty string", "", ""},
		{"just tilde", "~", "~"},
		{"tilde with slash", "~/", home + "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := expandHome(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoad_YAMLPathsExpandHome(t *testing.T) {
	// Create a temp YAML config file with ~ paths
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
workspace_root: ~/.seaturt/workspaces
db_path: ~/.seaturt/data.db
server_port: 9999
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0644)
	require.NoError(t, err)

	// Point Load() to our temp config
	t.Setenv("CONFIG_PATH", configPath)
	// Clear any env overrides that might interfere
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("DB_PATH", "")

	cfg := Load()

	home, _ := os.UserHomeDir()

	// Verify ~ was expanded to absolute path (the bug we fixed)
	assert.True(t, strings.HasPrefix(cfg.WorkspaceRoot, home),
		"WorkspaceRoot should start with home dir, got: %s", cfg.WorkspaceRoot)
	assert.False(t, strings.HasPrefix(cfg.WorkspaceRoot, "~"),
		"WorkspaceRoot should not start with ~, got: %s", cfg.WorkspaceRoot)
	assert.Equal(t, home+"/.seaturt/workspaces", cfg.WorkspaceRoot)

	assert.True(t, strings.HasPrefix(cfg.DBPath, home),
		"DBPath should start with home dir, got: %s", cfg.DBPath)
	assert.False(t, strings.HasPrefix(cfg.DBPath, "~"),
		"DBPath should not start with ~, got: %s", cfg.DBPath)
	assert.Equal(t, home+"/.seaturt/data.db", cfg.DBPath)

	// Verify other config was loaded
	assert.Equal(t, 9999, cfg.ServerPort)
}

func TestLoad_AbsolutePathsUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
workspace_root: /var/data/workspaces
db_path: /var/data/data.db
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0644)
	require.NoError(t, err)

	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("DB_PATH", "")

	cfg := Load()

	assert.Equal(t, "/var/data/workspaces", cfg.WorkspaceRoot)
	assert.Equal(t, "/var/data/data.db", cfg.DBPath)
}

func TestLoad_EnvOverrideTakePrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
workspace_root: ~/.seaturt/workspaces
db_path: ~/.seaturt/data.db
`
	err := os.WriteFile(configPath, []byte(yamlContent), 0644)
	require.NoError(t, err)

	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("WORKSPACE_ROOT", "~/custom/ws")
	t.Setenv("DB_PATH", "~/custom/db")

	cfg := Load()

	home, _ := os.UserHomeDir()

	// Env vars should override YAML, and ~ should still be expanded
	assert.Equal(t, home+"/custom/ws", cfg.WorkspaceRoot)
	assert.Equal(t, home+"/custom/db", cfg.DBPath)
}

func TestLoad_DefaultsWhenNoConfig(t *testing.T) {
	// Point to nonexistent config
	t.Setenv("CONFIG_PATH", "/nonexistent/config.yaml")
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("LLM_BASE_URL", "")

	cfg := Load()

	home, _ := os.UserHomeDir()

	// Should use defaults with ~ expanded
	assert.Equal(t, home+"/.seaturt/workspaces", cfg.WorkspaceRoot)
	assert.Equal(t, home+"/.seaturt/data.db", cfg.DBPath)
	assert.Equal(t, 8080, cfg.ServerPort)
}
