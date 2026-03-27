package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to build an SSE stream string from lines
func buildSSEStream(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func makeContentDelta(id, text string) string {
	delta := StreamDelta{
		ID: id,
		Choices: []Choice{
			{
				Index: 0,
				Delta: &ChatMessage{
					Role:    "assistant",
					Content: Content{NewTextContent(text)},
				},
			},
		},
	}
	data, _ := json.Marshal(delta)
	return string(data)
}

func makeFinishDelta(id, reason string) string {
	delta := StreamDelta{
		ID: id,
		Choices: []Choice{
			{
				Index:        0,
				FinishReason: reason,
			},
		},
	}
	data, _ := json.Marshal(delta)
	return string(data)
}

func TestConsumeSSE_DataWithSpace(t *testing.T) {
	t.Parallel()
	// Standard SSE format: "data: {json}"
	stream := buildSSEStream(
		"data: "+makeContentDelta("1", "Hello"),
		"",
		"data: "+makeFinishDelta("1", "stop"),
		"",
		"data: [DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Hello", resp.Choices[0].Message.Content.String())
	assert.Equal(t, "stop", resp.Choices[0].FinishReason)
}

func TestConsumeSSE_DataWithoutSpace(t *testing.T) {
	t.Parallel()
	// Compact SSE format: "data:{json}" (no space) — the format that caused the bug
	stream := buildSSEStream(
		"data:"+makeContentDelta("1", "Hello"),
		"",
		"data:"+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Hello", resp.Choices[0].Message.Content.String())
	assert.Equal(t, "stop", resp.Choices[0].FinishReason)
}

func TestConsumeSSE_ChineseUTF8Content(t *testing.T) {
	t.Parallel()
	// Test with Chinese content to ensure UTF-8 handling is correct
	chineseText := "你好！有什么我可以帮助你的吗？😊"
	stream := buildSSEStream(
		"data:"+makeContentDelta("1", chineseText),
		"",
		"data:"+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, chineseText, resp.Choices[0].Message.Content.String())
}

func TestConsumeSSE_MultipleDeltas(t *testing.T) {
	t.Parallel()
	// Multiple content deltas should be concatenated
	stream := buildSSEStream(
		"data:"+makeContentDelta("1", "Hello"),
		"",
		"data:"+makeContentDelta("1", " World"),
		"",
		"data:"+makeContentDelta("1", "!"),
		"",
		"data:"+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Hello World!", resp.Choices[0].Message.Content.String())
}

func TestConsumeSSE_ToolCalls(t *testing.T) {
	t.Parallel()
	// SSE with tool calls
	toolDelta := StreamDelta{
		ID: "1",
		Choices: []Choice{
			{
				Index: 0,
				Delta: &ChatMessage{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "shell_exec",
								Arguments: `{"command":"echo hi"}`,
							},
						},
					},
				},
			},
		},
	}
	toolData, _ := json.Marshal(toolDelta)

	stream := buildSSEStream(
		"data:"+string(toolData),
		"",
		"data:"+makeFinishDelta("1", "tool_calls"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	assert.Equal(t, "shell_exec", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	assert.Equal(t, "tool_calls", resp.Choices[0].FinishReason)
}

func TestConsumeSSE_IgnoresNonDataLines(t *testing.T) {
	t.Parallel()
	// SSE stream with comments and event types that should be ignored
	stream := buildSSEStream(
		": this is a comment",
		"event: message",
		"data:"+makeContentDelta("1", "Hello"),
		"",
		"retry: 3000",
		"data:"+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Hello", resp.Choices[0].Message.Content.String())
}

func TestConsumeSSE_StreamCallback(t *testing.T) {
	t.Parallel()
	stream := buildSSEStream(
		"data:"+makeContentDelta("1", "Hi"),
		"",
		"data:"+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	var callbackCount int
	resp, err := client.consumeSSE(strings.NewReader(stream), func(delta StreamDelta) error {
		callbackCount++
		return nil
	}, nil)
	require.NoError(t, err)

	assert.Equal(t, "Hi", resp.Choices[0].Message.Content.String())
	assert.Equal(t, 2, callbackCount) // content delta + finish delta
}

func TestConsumeSSE_EmptyStream(t *testing.T) {
	t.Parallel()
	stream := buildSSEStream(
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Empty(t, resp.Choices[0].Message.Content.String())
}

func TestConsumeSSE_MixedSpaceFormats(t *testing.T) {
	t.Parallel()
	// Mix of "data: " and "data:" in the same stream
	stream := buildSSEStream(
		"data: "+makeContentDelta("1", "Part1"),
		"",
		"data:"+makeContentDelta("1", "Part2"),
		"",
		"data: "+makeFinishDelta("1", "stop"),
		"",
		"data:[DONE]",
	)

	client := NewClient("http://test", "key", "model", "", nil, nil)
	resp, err := client.consumeSSE(strings.NewReader(stream), nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Part1Part2", resp.Choices[0].Message.Content.String())
}
