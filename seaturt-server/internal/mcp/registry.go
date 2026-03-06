package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ServerDef describes an MCP Server and its tools, loaded from YAML.
type ServerDef struct {
	Name        string           `yaml:"name" json:"name"`
	Command     string           `yaml:"command" json:"command"`
	Description string           `yaml:"description" json:"description"`
	Enabled     bool             `yaml:"enabled" json:"enabled"`
	Tools       []ToolDefinition `yaml:"tools" json:"tools"`
}

// yamlToolDef is an intermediate struct for YAML deserialization.
// We need this because InputSchema is `any` in ToolDefinition but YAML
// deserializes it as map[string]any, which needs to round-trip through
// JSON for consistent handling.
type yamlToolDef struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	InputSchema map[string]any `yaml:"inputSchema"`
}

// yamlServerDef is used for YAML parsing before converting to ServerDef.
type yamlServerDef struct {
	Name        string        `yaml:"name"`
	Command     string        `yaml:"command"`
	Description string        `yaml:"description"`
	Enabled     bool          `yaml:"enabled"`
	Tools       []yamlToolDef `yaml:"tools"`
}

// ToolRegistry reads tool definitions from YAML files in the workspace.
// It builds a Server → Tools mapping without starting any MCP Server process.
type ToolRegistry struct {
	mu      sync.RWMutex
	servers map[string]*ServerDef // server name → definition
	dir     string               // directory the YAML files were loaded from
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		servers: make(map[string]*ServerDef),
	}
}

// LoadFromDir reads all *.yaml files in the given directory and builds the registry.
func (r *ToolRegistry) LoadFromDir(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.dir = dir
	r.servers = make(map[string]*ServerDef)

	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("glob yaml files: %w", err)
	}

	for _, path := range matches {
		def, err := parseServerYAML(path)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
		}

		if !def.Enabled {
			continue
		}

		if def.Name == "" {
			return fmt.Errorf("parse %s: missing 'name' field", filepath.Base(path))
		}

		if _, exists := r.servers[def.Name]; exists {
			return fmt.Errorf("duplicate server name: %s", def.Name)
		}

		r.servers[def.Name] = def
	}

	return nil
}

// parseServerYAML reads a single YAML file and converts it to a ServerDef.
func parseServerYAML(path string) (*ServerDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw yamlServerDef
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	def := &ServerDef{
		Name:        raw.Name,
		Command:     raw.Command,
		Description: raw.Description,
		Enabled:     raw.Enabled,
		Tools:       make([]ToolDefinition, 0, len(raw.Tools)),
	}

	for _, t := range raw.Tools {
		// Convert map[string]any InputSchema to a JSON-friendly form
		// so that it round-trips consistently through JSON serialization.
		var schema any
		if t.InputSchema != nil {
			b, err := json.Marshal(t.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("marshal inputSchema for tool %s: %w", t.Name, err)
			}
			var unmarshaled any
			if err := json.Unmarshal(b, &unmarshaled); err != nil {
				return nil, fmt.Errorf("unmarshal inputSchema for tool %s: %w", t.Name, err)
			}
			schema = unmarshaled
		}

		def.Tools = append(def.Tools, ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	return def, nil
}

// AllTools returns all tool definitions from all enabled servers.
// The returned ToolDefinition.Name is in "mcpname-toolname" format, e.g. "core-shell_exec".
func (r *ToolRegistry) AllTools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []ToolDefinition
	for _, srv := range r.servers {
		for _, t := range srv.Tools {
			tools = append(tools, ToolDefinition{
				Name:        srv.Name + "-" + t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return tools
}

// GetServer returns the server definition for a given MCP server name.
func (r *ToolRegistry) GetServer(serverName string) (*ServerDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	srv, ok := r.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("unknown server: %s", serverName)
	}
	return srv, nil
}

// ServerNames returns the names of all enabled servers.
func (r *ToolRegistry) ServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.servers))
	for name := range r.servers {
		names = append(names, name)
	}
	return names
}

// Reload re-reads the YAML directory and rebuilds the registry.
// Used for future hot-reload support.
func (r *ToolRegistry) Reload() error {
	r.mu.RLock()
	dir := r.dir
	r.mu.RUnlock()

	if dir == "" {
		return fmt.Errorf("no directory loaded, call LoadFromDir first")
	}
	return r.LoadFromDir(dir)
}

// SplitToolName splits "mcpname-toolname" into server name and original tool name.
// It splits on the first "-": core-shell_exec → ("core", "shell_exec").
func SplitToolName(qualified string) (serverName, toolName string, err error) {
	idx := strings.Index(qualified, "-")
	if idx <= 0 || idx >= len(qualified)-1 {
		return "", "", fmt.Errorf("invalid tool name format: %q (expected 'mcpname-toolname')", qualified)
	}
	return qualified[:idx], qualified[idx+1:], nil
}
