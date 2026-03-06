//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-55: CORS preflight (OPTIONS) returns correct headers for allowed origin.
func TestCORS_Preflight(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	req, err := http.NewRequest("OPTIONS", ts.URL+"/api/agents", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// OPTIONS should return 204 No Content
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Check CORS headers
	assert.Equal(t, "http://localhost:5173", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "POST")
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Content-Type")
}

// IT-56: CORS headers are present on normal responses for allowed origin.
func TestCORS_NormalRequest(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	req, err := http.NewRequest("GET", ts.URL+"/api/models", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:3000")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "http://localhost:3000", resp.Header.Get("Access-Control-Allow-Origin"))
}

// IT-57: CORS headers are not present for non-localhost origins.
func TestCORS_DisallowedOrigin(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	req, err := http.NewRequest("GET", ts.URL+"/api/models", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.com")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Should NOT have CORS header for disallowed origin
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

// IT-58: CORS allows 127.0.0.1 origin.
func TestCORS_Localhost127(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	req, err := http.NewRequest("GET", ts.URL+"/api/models", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://127.0.0.1:5173")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "http://127.0.0.1:5173", resp.Header.Get("Access-Control-Allow-Origin"))
}
