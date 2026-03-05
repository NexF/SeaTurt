//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/seaturt/server/internal/config"
	"github.com/stretchr/testify/assert"
)

// E2E-05: Config loading — verifies real config.yaml is loaded correctly
func TestE2E_ConfigLoading(t *testing.T) {
	cfg := config.Load()

	// WorkspaceRoot should never contain ~ (must be expanded)
	assert.False(t, strings.HasPrefix(cfg.WorkspaceRoot, "~"),
		"WorkspaceRoot should not start with ~, got: %s", cfg.WorkspaceRoot)

	// DBPath should never contain ~ (must be expanded)
	assert.False(t, strings.HasPrefix(cfg.DBPath, "~"),
		"DBPath should not start with ~, got: %s", cfg.DBPath)

	// WorkspaceRoot should be an absolute path
	assert.True(t, strings.HasPrefix(cfg.WorkspaceRoot, "/"),
		"WorkspaceRoot should be absolute, got: %s", cfg.WorkspaceRoot)

	// DBPath should be an absolute path
	assert.True(t, strings.HasPrefix(cfg.DBPath, "/"),
		"DBPath should be absolute, got: %s", cfg.DBPath)

	// Providers should be loaded
	assert.NotEmpty(t, cfg.Providers, "should have at least one LLM provider")
	assert.NotEmpty(t, cfg.DefaultProvider, "should have a default provider")
	assert.NotEmpty(t, cfg.DefaultModel, "should have a default model")

	// Resolve LLM should work
	endpoint, err := cfg.ResolveLLM("", "")
	assert.NoError(t, err)
	assert.NotEmpty(t, endpoint.BaseURL, "endpoint should have a base URL")
}
