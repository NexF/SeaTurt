package llm

import "encoding/json"

// ContentBlock is the internal unified content block format for SeaTurt.
// It can represent text, image, or file content.
type ContentBlock struct {
	Type  string     `json:"type"`            // "text" | "image" | "file"
	Text  string     `json:"text,omitempty"`  // type=text
	Image *ImageData `json:"image,omitempty"` // type=image
	File  *FileData  `json:"file,omitempty"`  // type=file
}

// ImageData holds base64-encoded image data.
type ImageData struct {
	Data     string `json:"data,omitempty"`      // base64 encoded (empty if stored to file)
	MimeType string `json:"mime_type"`           // image/png, image/jpeg, etc.
	Detail   string `json:"detail,omitempty"`    // "auto" | "low" | "high" (OpenAI only)
	FilePath string `json:"file_path,omitempty"` // path to file on disk (for large images)
}

// FileData holds base64-encoded file data.
type FileData struct {
	Name     string `json:"name"`
	Data     string `json:"data"`      // base64 encoded
	MimeType string `json:"mime_type"`
}

// NewTextContent creates a text ContentBlock.
func NewTextContent(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// NewImageContent creates an image ContentBlock.
func NewImageContent(data, mimeType string) ContentBlock {
	return ContentBlock{
		Type:  "image",
		Image: &ImageData{Data: data, MimeType: mimeType},
	}
}

// Content wraps []ContentBlock with custom JSON marshaling.
// When serialized: single text block → plain string; otherwise → []ContentBlock.
// When deserialized: plain string → wrapped as []ContentBlock{{Type:"text"}}.
type Content []ContentBlock

// MarshalJSON implements json.Marshaler.
// If Content has exactly one text block, outputs a plain JSON string.
func (c Content) MarshalJSON() ([]byte, error) {
	if len(c) == 1 && c[0].Type == "text" {
		return json.Marshal(c[0].Text)
	}
	// Marshal as array of ContentBlock
	type blocks []ContentBlock
	return json.Marshal(blocks(c))
}

// UnmarshalJSON implements json.Unmarshaler.
// Accepts either a JSON string or an array of ContentBlock.
func (c *Content) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*c = Content{{Type: "text", Text: s}}
		return nil
	}
	// Try array of ContentBlock
	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	*c = Content(blocks)
	return nil
}

// TextOnly returns true if all blocks are text type.
func (c Content) TextOnly() bool {
	for _, b := range c {
		if b.Type != "text" {
			return false
		}
	}
	return true
}

// String returns the concatenated text content (ignoring non-text blocks).
func (c Content) String() string {
	var s string
	for _, b := range c {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

// HasType returns true if any block has the given type.
func (c Content) HasType(t string) bool {
	for _, b := range c {
		if b.Type == t {
			return true
		}
	}
	return false
}

// ContentTypes returns the unique set of content types present.
func (c Content) ContentTypes() []string {
	seen := make(map[string]bool)
	var types []string
	for _, b := range c {
		if !seen[b.Type] {
			seen[b.Type] = true
			types = append(types, b.Type)
		}
	}
	return types
}
