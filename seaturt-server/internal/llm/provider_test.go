package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-13: multimodal (text + image) → OpenAI format
func TestOpenAIFormatter_TextPlusImage(t *testing.T) {
	f := &OpenAIFormatter{}
	blocks := []ContentBlock{
		NewTextContent("describe this image"),
		NewImageContent("aGVsbG8=", "image/png"),
	}

	result, err := f.FormatContent(blocks)
	require.NoError(t, err)

	parts, ok := result.([]any)
	require.True(t, ok, "mixed content should return []any")
	require.Len(t, parts, 2)

	// Verify text part
	data, _ := json.Marshal(parts[0])
	var textPart map[string]any
	json.Unmarshal(data, &textPart)
	assert.Equal(t, "text", textPart["type"])
	assert.Equal(t, "describe this image", textPart["text"])

	// Verify image part
	data, _ = json.Marshal(parts[1])
	var imgPart map[string]any
	json.Unmarshal(data, &imgPart)
	assert.Equal(t, "image_url", imgPart["type"])
	imgURL := imgPart["image_url"].(map[string]any)
	assert.Contains(t, imgURL["url"], "data:image/png;base64,aGVsbG8=")
	assert.Equal(t, "auto", imgURL["detail"])
}

// IT-13b: pure text → OpenAI format returns plain string
func TestOpenAIFormatter_PureText(t *testing.T) {
	t.Parallel()
	f := &OpenAIFormatter{}
	blocks := []ContentBlock{
		NewTextContent("hello world"),
	}

	result, err := f.FormatContent(blocks)
	require.NoError(t, err)

	s, ok := result.(string)
	require.True(t, ok, "pure text should return string")
	assert.Equal(t, "hello world", s)
}

// IT-14: multimodal (text + image) → Anthropic format
func TestAnthropicFormatter_TextPlusImage(t *testing.T) {
	f := &AnthropicFormatter{}
	blocks := []ContentBlock{
		NewTextContent("what is in this picture?"),
		NewImageContent("aGVsbG8=", "image/jpeg"),
	}

	result, err := f.FormatContent(blocks)
	require.NoError(t, err)

	parts, ok := result.([]any)
	require.True(t, ok, "mixed content should return []any")
	require.Len(t, parts, 2)

	// Verify text part
	data, _ := json.Marshal(parts[0])
	var textPart map[string]any
	json.Unmarshal(data, &textPart)
	assert.Equal(t, "text", textPart["type"])
	assert.Equal(t, "what is in this picture?", textPart["text"])

	// Verify image part — Anthropic uses source.type=base64
	data, _ = json.Marshal(parts[1])
	var imgPart map[string]any
	json.Unmarshal(data, &imgPart)
	assert.Equal(t, "image", imgPart["type"])
	source := imgPart["source"].(map[string]any)
	assert.Equal(t, "base64", source["type"])
	assert.Equal(t, "image/jpeg", source["media_type"])
	assert.Equal(t, "aGVsbG8=", source["data"])
}

// IT-14b: pure text → Anthropic format returns plain string
func TestAnthropicFormatter_PureText(t *testing.T) {
	f := &AnthropicFormatter{}
	blocks := []ContentBlock{
		NewTextContent("hello"),
	}

	result, err := f.FormatContent(blocks)
	require.NoError(t, err)

	s, ok := result.(string)
	require.True(t, ok, "pure text should return string")
	assert.Equal(t, "hello", s)
}
