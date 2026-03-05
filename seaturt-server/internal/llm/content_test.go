package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-12: pure text content serialization
func TestContent_PureTextJSON(t *testing.T) {
	t.Parallel()
	content := Content{NewTextContent("hello")}

	// Marshal: single text block → plain string
	data, err := json.Marshal(content)
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, string(data))

	// Unmarshal back
	var decoded Content
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, "text", decoded[0].Type)
	assert.Equal(t, "hello", decoded[0].Text)
}

// IT-12b: multimodal content serialization
func TestContent_MultimodalJSON(t *testing.T) {
	content := Content{
		NewTextContent("describe this"),
		NewImageContent("aGVsbG8=", "image/png"),
	}

	data, err := json.Marshal(content)
	require.NoError(t, err)

	// Should be array
	var raw []json.RawMessage
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)
	assert.Len(t, raw, 2)

	// Unmarshal back
	var decoded Content
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
	assert.Equal(t, "text", decoded[0].Type)
	assert.Equal(t, "describe this", decoded[0].Text)
	assert.Equal(t, "image", decoded[1].Type)
	assert.Equal(t, "aGVsbG8=", decoded[1].Image.Data)
	assert.Equal(t, "image/png", decoded[1].Image.MimeType)
}

// IT-12c: backward compat — unmarshal plain string
func TestContent_UnmarshalPlainString(t *testing.T) {
	var content Content
	err := json.Unmarshal([]byte(`"hello world"`), &content)
	require.NoError(t, err)
	require.Len(t, content, 1)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "hello world", content[0].Text)
}

// IT-12d: Content helper methods
func TestContent_Helpers(t *testing.T) {
	textOnly := Content{NewTextContent("a"), NewTextContent("b")}
	assert.True(t, textOnly.TextOnly())
	assert.Equal(t, "ab", textOnly.String())
	assert.True(t, textOnly.HasType("text"))
	assert.False(t, textOnly.HasType("image"))
	assert.Equal(t, []string{"text"}, textOnly.ContentTypes())

	mixed := Content{NewTextContent("a"), NewImageContent("data", "image/png")}
	assert.False(t, mixed.TextOnly())
	assert.Equal(t, "a", mixed.String())
	assert.True(t, mixed.HasType("image"))
	assert.ElementsMatch(t, []string{"text", "image"}, mixed.ContentTypes())
}

// IT-21: image content block structure validation
func TestImageContentBlock(t *testing.T) {
	t.Parallel()
	block := NewImageContent("aGVsbG8=", "image/png")
	assert.Equal(t, "image", block.Type)
	require.NotNil(t, block.Image)
	assert.Equal(t, "aGVsbG8=", block.Image.Data)
	assert.Equal(t, "image/png", block.Image.MimeType)
	assert.Empty(t, block.Image.FilePath)
}

// IT-21b: ImageData with FilePath (externalized)
func TestImageDataWithFilePath(t *testing.T) {
	block := ContentBlock{
		Type: "image",
		Image: &ImageData{
			MimeType: "image/jpeg",
			FilePath: "/tmp/test.jpg",
		},
	}

	// Data should be empty when externalized
	assert.Empty(t, block.Image.Data)
	assert.Equal(t, "/tmp/test.jpg", block.Image.FilePath)

	// JSON roundtrip preserves FilePath
	data, err := json.Marshal(block)
	require.NoError(t, err)

	var decoded ContentBlock
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "image", decoded.Type)
	assert.Equal(t, "/tmp/test.jpg", decoded.Image.FilePath)
	assert.Empty(t, decoded.Image.Data)
}
