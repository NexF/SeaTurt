//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/seaturt/server/internal/llm"
)

// MockLLMResponse represents a pre-configured LLM response.
type MockLLMResponse struct {
	Content   string
	ToolCalls []llm.ToolCall
}

// MockLLMServer is an HTTP server that simulates an OpenAI-compatible LLM API.
// It returns pre-configured responses in sequence.
type MockLLMServer struct {
	mu             sync.Mutex
	responses      []MockLLMResponse
	callIndex      int
	server         *httptest.Server
	CallCount      int
	requestCapture func(body []byte) // optional callback to capture raw request body
}

// NewMockLLMServer creates and starts a mock LLM HTTP server.
func NewMockLLMServer(responses []MockLLMResponse) *MockLLMServer {
	m := &MockLLMServer{
		responses: responses,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", m.handleChatCompletion)
	m.server = httptest.NewServer(mux)

	return m
}

// BaseURL returns the base URL of the mock server.
func (m *MockLLMServer) BaseURL() string {
	return m.server.URL
}

// Close shuts down the mock server.
func (m *MockLLMServer) Close() {
	m.server.Close()
}

// SetRequestCapture sets a callback that receives the raw request body for each LLM call.
func (m *MockLLMServer) SetRequestCapture(fn func(body []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCapture = fn
}

// NewClient creates an LLM client pointing to this mock server.
func (m *MockLLMServer) NewClient() *llm.Client {
	return llm.NewClient(m.server.URL, "test-key", "test-model", "openai-completions", nil, nil)
}

// mockChatRequest is used to decode the incoming request (wire format).
type mockChatRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
}

func (m *MockLLMServer) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	// Read the full body first for potential capture
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallCount++

	// Invoke request capture callback if set
	if m.requestCapture != nil {
		m.requestCapture(bodyBytes)
	}

	if m.callIndex >= len(m.responses) {
		http.Error(w, "no more mock responses", http.StatusInternalServerError)
		return
	}

	mockResp := m.responses[m.callIndex]
	m.callIndex++

	// Check if streaming is requested
	var req mockChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Stream {
		m.handleStreaming(w, mockResp)
		return
	}

	// Non-streaming response
	msg := llm.ChatMessage{
		Role:      "assistant",
		Content:   llm.Content{llm.NewTextContent(mockResp.Content)},
		ToolCalls: mockResp.ToolCalls,
	}

	finishReason := "stop"
	if len(mockResp.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	resp := llm.ChatResponse{
		ID: fmt.Sprintf("mock-%d", m.callIndex),
		Choices: []llm.Choice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *MockLLMServer) handleStreaming(w http.ResponseWriter, mockResp MockLLMResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// NOTE: Use "data:%s" (no space after colon) to match real upstream LLM behavior.
	// Many LLM providers (e.g. gongfeng gateway) emit SSE without the optional space.
	// This ensures our SSE parser is tested against the real-world format.

	// Send content as a single delta
	if mockResp.Content != "" {
		delta := llm.StreamDelta{
			ID: fmt.Sprintf("mock-stream-%d", m.callIndex),
			Choices: []llm.Choice{
				{
					Index: 0,
					Delta: &llm.ChatMessage{
						Role:    "assistant",
						Content: llm.Content{llm.NewTextContent(mockResp.Content)},
					},
				},
			},
		}
		data, _ := json.Marshal(delta)
		fmt.Fprintf(w, "data:%s\n\n", data)
		flusher.Flush()
	}

	// Send tool calls if any
	if len(mockResp.ToolCalls) > 0 {
		delta := llm.StreamDelta{
			ID: fmt.Sprintf("mock-stream-%d", m.callIndex),
			Choices: []llm.Choice{
				{
					Index: 0,
					Delta: &llm.ChatMessage{
						Role:      "assistant",
						ToolCalls: mockResp.ToolCalls,
					},
				},
			},
		}
		data, _ := json.Marshal(delta)
		fmt.Fprintf(w, "data:%s\n\n", data)
		flusher.Flush()
	}

	// Send finish reason
	finishReason := "stop"
	if len(mockResp.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	finishDelta := llm.StreamDelta{
		ID: fmt.Sprintf("mock-stream-%d", m.callIndex),
		Choices: []llm.Choice{
			{
				Index:        0,
				FinishReason: finishReason,
			},
		},
	}
	data, _ := json.Marshal(finishDelta)
	fmt.Fprintf(w, "data:%s\n\n", data)
	flusher.Flush()

	// Send [DONE]
	fmt.Fprintf(w, "data:[DONE]\n\n")
	flusher.Flush()
}
