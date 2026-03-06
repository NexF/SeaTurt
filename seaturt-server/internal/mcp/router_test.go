package mcp

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_AllTools(t *testing.T) {
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

	// Executor is nil since we won't call Route
	router := NewRouter(reg, nil)

	tools := router.AllTools()
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

func TestRouter_ToolNames(t *testing.T) {
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

	router := NewRouter(reg, nil)

	names := router.ToolNames()
	assert.Equal(t, []string{"core-shell_exec"}, names)
}

func TestRouter_Route_UnknownTool(t *testing.T) {
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

	router := NewRouter(reg, nil)

	// Invalid format
	_, err = router.Route(nil, "notool", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tool name format")

	// Unknown server
	_, err = router.Route(nil, "unknown-shell_exec", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown server")
}
