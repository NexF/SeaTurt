package agent

import (
	"testing"

	"github.com/seaturt/server/internal/llm"
	"github.com/seaturt/server/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-17: MCP Tool returns image → correctly converts to ContentBlock{Type:"image"}
func TestFormatToolResult_ImageContent(t *testing.T) {
	t.Parallel()
	result := &mcp.CallToolResult{
		Content: []mcp.ToolContent{
			{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
		},
	}

	blocks := formatToolResult(result)
	require.Len(t, blocks, 1)
	assert.Equal(t, "image", blocks[0].Type)
	require.NotNil(t, blocks[0].Image)
	assert.Equal(t, "aGVsbG8=", blocks[0].Image.Data)
	assert.Equal(t, "image/png", blocks[0].Image.MimeType)
}

// IT-18: MCP Tool returns mixed (text + image) → complete pass-through
func TestFormatToolResult_MixedContent(t *testing.T) {
	t.Parallel()
	result := &mcp.CallToolResult{
		Content: []mcp.ToolContent{
			{Type: "text", Text: "Here is the screenshot:"},
			{Type: "image", Data: "cG5nZGF0YQ==", MimeType: "image/png"},
		},
	}

	blocks := formatToolResult(result)
	require.Len(t, blocks, 2)

	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "Here is the screenshot:", blocks[0].Text)

	assert.Equal(t, "image", blocks[1].Type)
	require.NotNil(t, blocks[1].Image)
	assert.Equal(t, "cG5nZGF0YQ==", blocks[1].Image.Data)
	assert.Equal(t, "image/png", blocks[1].Image.MimeType)
}

// IT-17b: nil result returns empty text block
func TestFormatToolResult_NilResult(t *testing.T) {
	t.Parallel()
	blocks := formatToolResult(nil)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "", blocks[0].Text)
}

// IT-17c: empty content returns empty text block
func TestFormatToolResult_EmptyContent(t *testing.T) {
	t.Parallel()
	result := &mcp.CallToolResult{Content: []mcp.ToolContent{}}
	blocks := formatToolResult(result)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
}

// IT-17d: text-only result
func TestFormatToolResult_TextOnly(t *testing.T) {
	t.Parallel()
	result := &mcp.CallToolResult{
		Content: []mcp.ToolContent{
			{Type: "text", Text: "command output here"},
		},
	}

	blocks := formatToolResult(result)
	require.Len(t, blocks, 1)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "command output here", blocks[0].Text)
}

// IT-18b: truncateContentBlocks only truncates text, passes image through
func TestTruncateContentBlocks(t *testing.T) {
	t.Parallel()
	blocks := llm.Content{
		llm.NewTextContent("a very long text that should be truncated"),
		llm.NewImageContent("aGVsbG8=", "image/png"),
	}

	truncated := truncateContentBlocks(blocks, 10)
	require.Len(t, truncated, 2)

	// Text should be truncated
	assert.LessOrEqual(t, len([]rune(truncated[0].Text)), 10+100) // 10 chars + truncation notice

	// Image should be untouched
	assert.Equal(t, "image", truncated[1].Type)
	assert.Equal(t, "aGVsbG8=", truncated[1].Image.Data)
}
