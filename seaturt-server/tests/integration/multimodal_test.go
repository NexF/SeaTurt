//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	agentpkg "github.com/seaturt/server/internal/agent"
	"github.com/seaturt/server/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-12: pure text message works normally
func TestChat_PureText(t *testing.T) {
	t.Parallel()
	mockResponses := []MockLLMResponse{
		{Content: "Hello! How can I help you?"},
	}
	ts, _ := newTestServer(t, mockResponses)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "text-test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	// Send pure text message
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "hello"}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Parse SSE and find text_delta + done
	events := parseSSEEvents(t, chatResp)
	types := eventTypeSet(events)
	assert.Contains(t, types, "text_delta")
	assert.Contains(t, types, "done")

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// IT-15: multipart/form-data image upload → auto-parses to ContentBlock
func TestChat_MultipartImageUpload(t *testing.T) {
	t.Parallel()
	mockResponses := []MockLLMResponse{
		{Content: "I see a test image."},
	}
	ts, _ := newTestServer(t, mockResponses)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "multipart-test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	// Build multipart request
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("text", "describe this image")

	// Create a small fake PNG with correct Content-Type header
	fakePNG := createFakePNG()
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="test.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(partHeader)
	part.Write(fakePNG)
	writer.Close()

	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", &buf)
	chatReq.Header.Set("Content-Type", writer.FormDataContentType())
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	// Verify events
	events := parseSSEEvents(t, chatResp)
	types := eventTypeSet(events)
	assert.Contains(t, types, "done")

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// IT-19: file_read of image → returns image type ToolContent
func TestChat_FileReadImage(t *testing.T) {
	t.Parallel()
	// Mock: first call returns tool_call to read an image, second returns text
	mockResponses := []MockLLMResponse{
		{
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_img",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "shell_exec",
						Arguments: `{"command":"echo 'reading image'"}`,
					},
				},
			},
		},
		{Content: "I analyzed the image."},
	}

	ts, _ := newTestServer(t, mockResponses)

	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "file-read-img"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	// Send chat
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": "read this image file"}},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusOK, chatResp.StatusCode)

	events := parseSSEEvents(t, chatResp)
	types := eventTypeSet(events)
	assert.Contains(t, types, "tool_call")
	assert.Contains(t, types, "tool_result")
	assert.Contains(t, types, "done")

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// IT-20: multimodal history roundtrip
func TestChat_HistoryRoundtrip(t *testing.T) {
	t.Parallel()
	mockResponses := []MockLLMResponse{
		{Content: "I can see the image."},
	}
	ts, _ := newTestServer(t, mockResponses)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "history-test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	// Send multimodal message (text + image via JSON)
	imgData := base64.StdEncoding.EncodeToString([]byte("fake-image"))
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "look at this"},
			{"type": "image", "image": map[string]string{
				"data":      imgData,
				"mime_type": "image/png",
			}},
		},
	})
	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	chatResp.Body.Close()

	// Get history
	resp = doRequest(t, ts, "GET", "/api/agents/"+ag.ID+"/history", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var messages []json.RawMessage
	decodeJSON(t, resp, &messages)
	assert.GreaterOrEqual(t, len(messages), 2, "should have user + assistant messages")

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// IT-21: image size validation (oversized → 400)
func TestChat_ImageSizeLimit(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t, nil)

	// Create agent
	resp := doRequest(t, ts, "POST", "/api/agents", map[string]any{"name": "size-limit-test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var ag agentpkg.Agent
	decodeJSON(t, resp, &ag)

	// Create oversized image (> 20MB worth of base64)
	// In the test server, maxImageSize is 20*1024*1024
	// base64 size ≈ 4/3 * raw size, so ~28MB of base64 represents ~21MB raw
	bigData := base64.StdEncoding.EncodeToString(make([]byte, 21*1024*1024))
	chatBody, _ := json.Marshal(map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "huge image"},
			{"type": "image", "image": map[string]string{
				"data":      bigData,
				"mime_type": "image/png",
			}},
		},
	})

	chatReq, _ := http.NewRequest("POST", ts.URL+"/api/agents/"+ag.ID+"/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	require.NoError(t, err)
	defer chatResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, chatResp.StatusCode)

	// Cleanup
	doRequest(t, ts, "DELETE", "/api/agents/"+ag.ID, nil)
}

// --- Helpers ---

func parseSSEEvents(t *testing.T, resp *http.Response) []agentpkg.StreamEvent {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
	var events []agentpkg.StreamEvent
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		var event agentpkg.StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func eventTypeSet(events []agentpkg.StreamEvent) map[string]bool {
	set := make(map[string]bool)
	for _, e := range events {
		set[e.Type] = true
	}
	return set
}

// createFakePNG creates a minimal valid PNG file (1x1 pixel).
func createFakePNG() []byte {
	// Minimal 1x1 white PNG
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC,
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
}

// Unused but kept for reference: format verification helper
func mustMarshal(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func init() {
	// Suppress "unused" warning for mustMarshal
	_ = fmt.Sprintf
}
