//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-60: File upload → list → read complete flow.
func TestFilesCRUD(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "files-test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// 1. Upload a file
	fileContent := "Hello, this is a test file.\n第二行中文测试。"
	uploadResp := uploadFile(t, ts, ag.ID, "test.txt", "", []byte(fileContent))
	require.Equal(t, http.StatusCreated, uploadResp.StatusCode)

	var uploadResult struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Name string `json:"name"`
	}
	decodeJSON(t, uploadResp, &uploadResult)
	assert.Equal(t, "test.txt", uploadResult.Name)
	assert.Equal(t, "test.txt", uploadResult.Path)
	assert.Greater(t, uploadResult.Size, int64(0))

	// 2. List files — should see the uploaded file
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/files", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var listResult struct {
		Files []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size"`
		} `json:"files"`
	}
	decodeJSON(t, resp, &listResult)

	// Find our uploaded file
	found := false
	for _, f := range listResult.Files {
		if f.Name == "test.txt" {
			found = true
			assert.False(t, f.IsDir)
			assert.Greater(t, f.Size, int64(0))
		}
	}
	assert.True(t, found, "uploaded file should appear in listing")

	// 3. Read the file
	req, err := http.NewRequest("GET", ts.URL+"/api/agents/"+ag.ID+"/files/test.txt", nil)
	require.NoError(t, err)
	readResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer readResp.Body.Close()

	assert.Equal(t, http.StatusOK, readResp.StatusCode)
	body, _ := io.ReadAll(readResp.Body)
	assert.Equal(t, fileContent, string(body))
}

// IT-61: Upload to subdirectory.
func TestFilesUpload_Subdirectory(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "files-subdir"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	// Upload to a subdirectory
	uploadResp := uploadFile(t, ts, ag.ID, "report.md", "docs", []byte("# Monthly Report"))
	require.Equal(t, http.StatusCreated, uploadResp.StatusCode)

	var uploadResult struct {
		Path string `json:"path"`
	}
	decodeJSON(t, uploadResp, &uploadResult)
	assert.Contains(t, uploadResult.Path, "docs")

	// List subdirectory
	req, err := http.NewRequest("GET", ts.URL+"/api/agents/"+ag.ID+"/files?path=docs", nil)
	require.NoError(t, err)
	listResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer listResp.Body.Close()

	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	var listResult struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	decodeJSON(t, listResp, &listResult)
	require.Len(t, listResult.Files, 1)
	assert.Equal(t, "report.md", listResult.Files[0].Name)
}

// IT-62: Read non-existent file returns 404.
func TestFilesRead_NotFound(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "files-404"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	req, err := http.NewRequest("GET", ts.URL+"/api/agents/"+ag.ID+"/files/nonexistent.txt", nil)
	require.NoError(t, err)
	readResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer readResp.Body.Close()

	assert.Equal(t, http.StatusNotFound, readResp.StatusCode)
}

// IT-63: Path traversal is blocked.
func TestFilesRead_PathTraversal(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "files-traversal"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)
	defer doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)

	req, err := http.NewRequest("GET", ts.URL+"/api/agents/"+ag.ID+"/files/../../../etc/passwd", nil)
	require.NoError(t, err)
	readResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer readResp.Body.Close()

	// Should be either 403 (path traversal blocked) or 404
	assert.True(t,
		readResp.StatusCode == http.StatusForbidden || readResp.StatusCode == http.StatusNotFound,
		"path traversal should be blocked, got %d", readResp.StatusCode)
}

// IT-64: List files on non-existent agent returns 404.
func TestFilesList_AgentNotFound(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	resp := doRequest(t, ts, "GET", "/api/agents/nonexistent/files", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- Helpers ---

// uploadFile sends a multipart file upload request.
func uploadFile(t *testing.T, ts *httptest.Server, agentID, filename, destPath string, content []byte) *http.Response {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if destPath != "" {
		_ = writer.WriteField("path", destPath)
	}

	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", ts.URL+"/api/agents/"+agentID+"/files", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func init() {
	// Suppress unused import warning
	_ = json.Marshal
}
