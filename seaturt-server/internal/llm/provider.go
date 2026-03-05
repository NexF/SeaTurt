package llm

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// ContentFormatter converts internal ContentBlock to a provider-specific API format.
type ContentFormatter interface {
	// FormatContent converts internal ContentBlocks to the provider's API format.
	// Returns either a string (pure text) or a provider-specific structure.
	FormatContent(blocks []ContentBlock) (any, error)
}

// GetFormatter returns the appropriate ContentFormatter for the given API type.
func GetFormatter(apiType string) ContentFormatter {
	switch apiType {
	case "anthropic-messages":
		return &AnthropicFormatter{}
	default:
		// "openai-completions" and unknown types default to OpenAI format
		return &OpenAIFormatter{}
	}
}

// --- OpenAI Formatter ---

// OpenAIFormatter formats content for OpenAI-compatible APIs.
type OpenAIFormatter struct{}

// openAITextPart is a text content part in OpenAI format.
type openAITextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// openAIImageURLPart is an image_url content part in OpenAI format.
type openAIImageURLPart struct {
	Type     string          `json:"type"`
	ImageURL openAIImageURL  `json:"image_url"`
}

// openAIImageURL holds the URL and detail level.
type openAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (f *OpenAIFormatter) FormatContent(blocks []ContentBlock) (any, error) {
	if len(blocks) == 0 {
		return "", nil
	}

	// Pure text optimization: return plain string
	allText := true
	for _, b := range blocks {
		if b.Type != "text" {
			allText = false
			break
		}
	}
	if allText {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
		}
		return sb.String(), nil
	}

	// Mixed content: return array of parts
	var parts []any
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, openAITextPart{Type: "text", Text: b.Text})
		case "image":
			if b.Image == nil {
				return nil, fmt.Errorf("image block missing image data")
			}
			dataURL := toDataURL(b.Image.Data, b.Image.MimeType)
			detail := b.Image.Detail
			if detail == "" {
				detail = "auto"
			}
			parts = append(parts, openAIImageURLPart{
				Type: "image_url",
				ImageURL: openAIImageURL{
					URL:    dataURL,
					Detail: detail,
				},
			})
		case "file":
			if b.File == nil {
				return nil, fmt.Errorf("file block missing file data")
			}
			// Files are sent as text with a note about the file
			parts = append(parts, openAITextPart{
				Type: "text",
				Text: fmt.Sprintf("[File: %s (%s)]", b.File.Name, b.File.MimeType),
			})
		default:
			return nil, fmt.Errorf("unsupported content type: %s", b.Type)
		}
	}
	return parts, nil
}

// --- Anthropic Formatter ---

// AnthropicFormatter formats content for Anthropic Claude API.
type AnthropicFormatter struct{}

// anthropicTextBlock is a text content block in Anthropic format.
type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicImageBlock is an image content block in Anthropic format.
type anthropicImageBlock struct {
	Type   string               `json:"type"`
	Source anthropicImageSource `json:"source"`
}

// anthropicImageSource holds the base64 image source.
type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func (f *AnthropicFormatter) FormatContent(blocks []ContentBlock) (any, error) {
	if len(blocks) == 0 {
		return "", nil
	}

	// Pure text optimization: return plain string
	allText := true
	for _, b := range blocks {
		if b.Type != "text" {
			allText = false
			break
		}
	}
	if allText {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
		}
		return sb.String(), nil
	}

	// Mixed content: return Anthropic-specific blocks
	var parts []any
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, anthropicTextBlock{Type: "text", Text: b.Text})
		case "image":
			if b.Image == nil {
				return nil, fmt.Errorf("image block missing image data")
			}
			parts = append(parts, anthropicImageBlock{
				Type: "image",
				Source: anthropicImageSource{
					Type:      "base64",
					MediaType: b.Image.MimeType,
					Data:      b.Image.Data,
				},
			})
		case "file":
			if b.File == nil {
				return nil, fmt.Errorf("file block missing file data")
			}
			parts = append(parts, anthropicTextBlock{
				Type: "text",
				Text: fmt.Sprintf("[File: %s (%s)]", b.File.Name, b.File.MimeType),
			})
		default:
			return nil, fmt.Errorf("unsupported content type: %s", b.Type)
		}
	}
	return parts, nil
}

// --- Helpers ---

// toDataURL converts base64 data + mime type to a data: URL.
// If the data already looks like a data: URL or https URL, returns it as-is.
func toDataURL(data, mimeType string) string {
	if strings.HasPrefix(data, "data:") || strings.HasPrefix(data, "http") {
		return data
	}
	// Ensure the data is valid base64 (strip any whitespace)
	data = strings.TrimSpace(data)
	// Verify it's valid base64
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		// Try URL-safe base64
		if _, err2 := base64.URLEncoding.DecodeString(data); err2 != nil {
			// Return as-is, might be raw data
			return data
		}
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, data)
}
