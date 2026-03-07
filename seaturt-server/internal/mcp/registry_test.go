package mcp

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

func writeTempYAML(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

// --- ToolRegistry tests ---

func TestLoadFromDir_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
description: "基础操作"
enabled: true
tools:
  - name: shell_exec
    description: "Execute a shell command"
    inputSchema:
      type: object
      properties:
        command:
          type: string
          description: "The shell command"
      required: ["command"]
  - name: file_read
    description: "Read file contents"
    inputSchema:
      type: object
      properties:
        path:
          type: string
      required: ["path"]
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	srv, err := reg.GetServer("core")
	require.NoError(t, err)
	assert.Equal(t, "core", srv.Name)
	assert.Equal(t, "mcp-server-core", srv.Command)
	assert.Equal(t, "基础操作", srv.Description)
	assert.True(t, srv.Enabled)
	assert.Len(t, srv.Tools, 2)
	assert.Equal(t, "shell_exec", srv.Tools[0].Name)
	assert.Equal(t, "file_read", srv.Tools[1].Name)

	// Verify InputSchema is properly parsed
	schema, ok := srv.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok, "InputSchema should be map[string]any")
	assert.Equal(t, "object", schema["type"])
}

func TestLoadFromDir_DisabledServer(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "disabled.yaml", `
name: disabled
command: mcp-disabled
enabled: false
tools:
  - name: foo
    description: "Foo tool"
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	_, err = reg.GetServer("disabled")
	assert.Error(t, err, "disabled server should not be loaded")
	assert.Empty(t, reg.AllTools())
}

func TestLoadFromDir_MultipleServers(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute shell"
`)
	writeTempYAML(t, dir, "desktop.yaml", `
name: desktop
command: mcp-server-desktop
enabled: true
tools:
  - name: screenshot
    description: "Take screenshot"
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	names := reg.ServerNames()
	sort.Strings(names)
	assert.Equal(t, []string{"core", "desktop"}, names)
}

func TestLoadFromDir_DuplicateServerName(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "a.yaml", `
name: core
command: mcp-a
enabled: true
tools: []
`)
	writeTempYAML(t, dir, "b.yaml", `
name: core
command: mcp-b
enabled: true
tools: []
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate server name")
}

func TestLoadFromDir_MissingName(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "bad.yaml", `
command: mcp-bad
enabled: true
tools: []
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'name' field")
}

func TestLoadFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)
	assert.Empty(t, reg.AllTools())
}

func TestLoadFromDir_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "bad.yaml", `{{{invalid yaml`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	assert.Error(t, err)
}

// --- AllTools with mcpname-toolname prefix ---

func TestAllTools_PrefixedNames(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute shell"
  - name: file_read
    description: "Read file"
`)
	writeTempYAML(t, dir, "desktop.yaml", `
name: desktop
command: mcp-server-desktop
enabled: true
tools:
  - name: screenshot
    description: "Take screenshot"
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	tools := reg.AllTools()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)

	assert.Equal(t, []string{
		"core-file_read",
		"core-shell_exec",
		"desktop-screenshot",
	}, names)
}

func TestAllTools_PreservesDescription(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute a shell command"
    inputSchema:
      type: object
      properties:
        command:
          type: string
      required: ["command"]
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	tools := reg.AllTools()
	require.Len(t, tools, 1)
	assert.Equal(t, "core-shell_exec", tools[0].Name)
	assert.Equal(t, "Execute a shell command", tools[0].Description)
	assert.NotNil(t, tools[0].InputSchema)
}

// --- SplitToolName tests ---

func TestSplitToolName_Valid(t *testing.T) {
	tests := []struct {
		input      string
		wantServer string
		wantTool   string
	}{
		{"core-shell_exec", "core", "shell_exec"},
		{"desktop-screenshot", "desktop", "screenshot"},
		{"browser-navigate", "browser", "navigate"},
		{"my-server-my-tool", "my", "server-my-tool"}, // splits on first "-"
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			server, tool, err := SplitToolName(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantTool, tool)
		})
	}
}

func TestSplitToolName_Invalid(t *testing.T) {
	tests := []string{
		"",
		"notool",
		"-leading",
		"trailing-",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			_, _, err := SplitToolName(tt)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid tool name format")
		})
	}
}

// --- Reload tests ---

func TestReload(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute shell"
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)
	assert.Len(t, reg.AllTools(), 1)

	// Add another tool to YAML
	writeTempYAML(t, dir, "core.yaml", `
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute shell"
  - name: file_read
    description: "Read file"
`)

	err = reg.Reload()
	require.NoError(t, err)
	assert.Len(t, reg.AllTools(), 2)
}

func TestReload_NoDir(t *testing.T) {
	reg := NewToolRegistry()
	err := reg.Reload()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no directory loaded")
}

// --- AddServer / RemoveServer tests ---

func TestAddServer(t *testing.T) {
	reg := NewToolRegistry()

	srv := &ServerDef{
		Name:    "custom",
		Command: "mcp-custom",
		Enabled: true,
		Tools: []ToolDefinition{
			{Name: "my_tool", Description: "A custom tool"},
		},
	}

	reg.AddServer("custom", srv)

	got, err := reg.GetServer("custom")
	require.NoError(t, err)
	assert.Equal(t, "custom", got.Name)
	assert.Len(t, got.Tools, 1)

	// AllTools should include it
	tools := reg.AllTools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "custom-my_tool", tools[0].Name)
}

func TestAddServer_Overwrite(t *testing.T) {
	reg := NewToolRegistry()

	srv1 := &ServerDef{
		Name: "test", Command: "mcp-v1", Enabled: true,
		Tools: []ToolDefinition{{Name: "old_tool", Description: "Old"}},
	}
	srv2 := &ServerDef{
		Name: "test", Command: "mcp-v2", Enabled: true,
		Tools: []ToolDefinition{{Name: "new_tool", Description: "New"}},
	}

	reg.AddServer("test", srv1)
	reg.AddServer("test", srv2) // overwrite

	got, err := reg.GetServer("test")
	require.NoError(t, err)
	assert.Equal(t, "mcp-v2", got.Command)
	assert.Equal(t, "new_tool", got.Tools[0].Name)
}

func TestRemoveServer(t *testing.T) {
	reg := NewToolRegistry()

	srv := &ServerDef{
		Name: "temp", Command: "mcp-temp", Enabled: true,
		Tools: []ToolDefinition{{Name: "t", Description: "T"}},
	}
	reg.AddServer("temp", srv)
	assert.Len(t, reg.AllTools(), 1)

	ok := reg.RemoveServer("temp")
	assert.True(t, ok)
	assert.Empty(t, reg.AllTools())

	_, err := reg.GetServer("temp")
	assert.Error(t, err)
}

func TestRemoveServer_NotFound(t *testing.T) {
	reg := NewToolRegistry()
	ok := reg.RemoveServer("nonexistent")
	assert.False(t, ok)
}

// --- GetServer tests ---

func TestGetServer_Unknown(t *testing.T) {
	reg := NewToolRegistry()
	_, err := reg.GetServer("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown server")
}

// --- InputSchema round-trip test ---

func TestInputSchema_YAMLToJSON(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "test.yaml", `
name: test
command: mcp-test
enabled: true
tools:
  - name: my_tool
    description: "A test tool"
    inputSchema:
      type: object
      properties:
        name:
          type: string
          description: "The name"
        count:
          type: integer
          description: "A count"
      required: ["name"]
`)

	reg := NewToolRegistry()
	err := reg.LoadFromDir(dir)
	require.NoError(t, err)

	srv, _ := reg.GetServer("test")
	schema, ok := srv.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "count")

	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"name"}, required)
}
