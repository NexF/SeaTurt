package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client calls an OpenAI-compatible chat completion API.
type Client struct {
	baseURL    string
	apiKey     string
	model      string
	apiType    string // "openai-completions", "anthropic-messages", etc.
	headers    map[string]string // custom per-request headers
	formatter  ContentFormatter
	httpClient *http.Client
}

// NewClient creates a new LLM client.
func NewClient(baseURL, apiKey, model, apiType string, headers map[string]string) *Client {
	if apiType == "" {
		apiType = "openai-completions"
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		apiType:   apiType,
		headers:   headers,
		formatter: GetFormatter(apiType),
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// Formatter returns the content formatter for this client.
func (c *Client) Formatter() ContentFormatter {
	return c.formatter
}

// --- Request / Response types (OpenAI-compatible) ---

// ChatMessage represents a message in the conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    Content    `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function name and arguments in a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolDef defines a tool for the chat completion request.
type ToolDef struct {
	Type     string     `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef is the function schema in a tool definition.
type FunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// chatRequestMessage is a single message in the wire-format request,
// where Content is already formatted for the target Provider (string or []any).
type chatRequestMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// chatRequestBody is the wire-format request body for chat completions.
type chatRequestBody struct {
	Model    string               `json:"model"`
	Messages []chatRequestMessage `json:"messages"`
	Tools    []ToolDef            `json:"tools,omitempty"`
	Stream   bool                 `json:"stream"`
}

// buildRequestBody converts internal ChatMessages to the wire format,
// applying the Provider's ContentFormatter to each message's Content.
func (c *Client) buildRequestBody(messages []ChatMessage, tools []ToolDef, stream bool) ([]byte, error) {
	wireMessages := make([]chatRequestMessage, 0, len(messages))
	for _, m := range messages {
		var formatted any
		if len(m.Content) > 0 {
			var err error
			formatted, err = c.formatter.FormatContent(m.Content)
			if err != nil {
				return nil, fmt.Errorf("format content for role=%s: %w", m.Role, err)
			}
		}
		wireMessages = append(wireMessages, chatRequestMessage{
			Role:       m.Role,
			Content:    formatted,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	return json.Marshal(chatRequestBody{
		Model:    c.model,
		Messages: wireMessages,
		Tools:    tools,
		Stream:   stream,
	})
}

// ChatResponse is the non-streaming response.
type ChatResponse struct {
	ID      string         `json:"id"`
	Choices []Choice       `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int          `json:"index"`
	Message      ChatMessage  `json:"message"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

// Usage reports token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamDelta is a single SSE chunk in a streaming response.
type StreamDelta struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
}

// --- API calls ---

// ChatCompletion performs a non-streaming chat completion.
func (c *Client) ChatCompletion(messages []ChatMessage, tools []ToolDef) (*ChatResponse, error) {
	body, err := c.buildRequestBody(messages, tools, false)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	slog.Info("llm request",
		"url", url,
		"model", c.model,
		"stream", false,
		"messages", len(messages),
		"tools", len(tools),
		"body_bytes", len(body),
	)

	start := time.Now()
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("llm request failed",
			"url", url,
			"model", c.model,
			"elapsed", elapsed,
			"error", err,
		)
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Error("llm api error",
			"url", url,
			"model", c.model,
			"status", resp.StatusCode,
			"elapsed", elapsed,
			"body_bytes", len(body),
			"response", truncateLog(string(respBody), 1000),
		)
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	slog.Info("llm response",
		"model", c.model,
		"elapsed", elapsed,
		"choices", len(chatResp.Choices),
		"usage", chatResp.Usage,
	)

	return &chatResp, nil
}

// StreamCallback is called for each SSE chunk. Return a non-nil error to abort.
type StreamCallback func(delta StreamDelta) error

// ChatCompletionStream performs a streaming chat completion.
// The callback is invoked for each SSE delta. After the stream ends,
// this returns the fully assembled ChatResponse.
func (c *Client) ChatCompletionStream(messages []ChatMessage, tools []ToolDef, cb StreamCallback) (*ChatResponse, error) {
	body, err := c.buildRequestBody(messages, tools, true)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	slog.Info("llm request",
		"url", url,
		"model", c.model,
		"stream", true,
		"messages", len(messages),
		"tools", len(tools),
		"body_bytes", len(body),
	)

	start := time.Now()
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		slog.Error("llm request failed",
			"url", url,
			"model", c.model,
			"elapsed", time.Since(start),
			"error", err,
		)
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Error("llm api error",
			"url", url,
			"model", c.model,
			"status", resp.StatusCode,
			"elapsed", time.Since(start),
			"body_bytes", len(body),
			"response", truncateLog(string(respBody), 1000),
		)
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(respBody))
	}

	slog.Info("llm stream started",
		"model", c.model,
		"ttfb", time.Since(start),
	)

	assembled, err := c.consumeSSE(resp.Body, cb)
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("llm stream error",
			"model", c.model,
			"elapsed", elapsed,
			"error", err,
		)
		return nil, err
	}

	slog.Info("llm stream completed",
		"model", c.model,
		"elapsed", elapsed,
		"choices", len(assembled.Choices),
		"usage", assembled.Usage,
	)

	return assembled, nil
}

// consumeSSE reads SSE lines, assembles deltas into a full ChatResponse.
func (c *Client) consumeSSE(r io.Reader, cb StreamCallback) (*ChatResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	assembled := &ChatResponse{}
	var contentBuilder strings.Builder
	toolCallsMap := make(map[int]*ToolCall) // index -> accumulated tool call
	var finishReason string

	for scanner.Scan() {
		line := scanner.Text()

		// SSE spec: "data:" may or may not be followed by a space
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ") // remove optional space
		if data == "[DONE]" {
			break
		}

		var delta StreamDelta
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			continue // skip malformed chunks
		}

		if assembled.ID == "" {
			assembled.ID = delta.ID
		}

		if cb != nil {
			if err := cb(delta); err != nil {
				return nil, err
			}
		}

		// Accumulate content and tool_calls from deltas
		for _, choice := range delta.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			if choice.Delta == nil {
				continue
			}
			if text := choice.Delta.Content.String(); text != "" {
				contentBuilder.WriteString(text)
			}
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsMap[choice.Index]
				if !ok {
					cp := tc
					toolCallsMap[choice.Index] = &cp
				} else {
					// Append arguments incrementally
					existing.Function.Arguments += tc.Function.Arguments
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}

	// Build assembled response
	msg := ChatMessage{
		Role:    "assistant",
		Content: Content{NewTextContent(contentBuilder.String())},
	}
	for i := 0; i < len(toolCallsMap); i++ {
		if tc, ok := toolCallsMap[i]; ok {
			msg.ToolCalls = append(msg.ToolCalls, *tc)
		}
	}

	assembled.Choices = []Choice{
		{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		},
	}

	return assembled, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	// Apply custom per-model headers (may override defaults)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

// truncateLog truncates a string for safe logging.
func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
