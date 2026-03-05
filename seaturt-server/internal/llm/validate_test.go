package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IT-16: send image to text-only model → returns error
func TestValidateContent_ImageToTextOnlyModel(t *testing.T) {
	blocks := []ContentBlock{
		NewTextContent("look at this"),
		NewImageContent("aGVsbG8=", "image/png"),
	}

	// Model with no input specified → defaults to text only
	err := ValidateContent("gpt-3.5-turbo", nil, blocks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support image input")

	// Model with explicit text-only
	err = ValidateContent("gpt-3.5-turbo", []string{"text"}, blocks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support image input")
}

// IT-16b: send image to multimodal model → passes
func TestValidateContent_ImageToMultimodalModel(t *testing.T) {
	blocks := []ContentBlock{
		NewTextContent("look at this"),
		NewImageContent("aGVsbG8=", "image/png"),
	}

	err := ValidateContent("gpt-4o", []string{"text", "image"}, blocks)
	assert.NoError(t, err)
}

// IT-16c: pure text to any model → always passes
func TestValidateContent_PureTextAlwaysPasses(t *testing.T) {
	blocks := []ContentBlock{
		NewTextContent("hello"),
	}

	assert.NoError(t, ValidateContent("any-model", nil, blocks))
	assert.NoError(t, ValidateContent("any-model", []string{"text"}, blocks))
	assert.NoError(t, ValidateContent("any-model", []string{"text", "image"}, blocks))
}

// IT-16d: empty content always passes
func TestValidateContent_EmptyContent(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ValidateContent("any-model", nil, nil))
	assert.NoError(t, ValidateContent("any-model", nil, []ContentBlock{}))
}
