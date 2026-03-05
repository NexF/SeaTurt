package llm

import (
	"fmt"
	"strings"
)

// ValidateContent checks whether the given content blocks are compatible with the model's
// declared input capabilities. Returns an error if unsupported content types are found.
// supportedInputs is the model's Input field (e.g. ["text", "image"]).
// If supportedInputs is empty, defaults to ["text"] only.
func ValidateContent(modelID string, supportedInputs []string, blocks []ContentBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	// Build allowed set
	allowed := make(map[string]bool)
	if len(supportedInputs) == 0 {
		allowed["text"] = true
	} else {
		for _, t := range supportedInputs {
			allowed[strings.ToLower(strings.TrimSpace(t))] = true
		}
	}

	// Check each block
	for _, b := range blocks {
		if !allowed[b.Type] {
			return fmt.Errorf("model %s does not support %s input", modelID, b.Type)
		}
	}
	return nil
}
