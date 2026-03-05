package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func toolShellExec(args map[string]any) CallToolResult {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return errorResult("missing or invalid 'command' argument")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = "/workspace"
	output, err := cmd.CombinedOutput()

	text := string(output)
	if err != nil {
		text += fmt.Sprintf("\nexit error: %v", err)
		return CallToolResult{
			Content: []ToolContent{{Type: "text", Text: text}},
			IsError: true,
		}
	}

	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

// imageTypes maps MIME types that should be returned as image content blocks.
var imageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func toolFileRead(args map[string]any) CallToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return errorResult("missing or invalid 'path' argument")
	}

	path = resolvePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(fmt.Sprintf("read file: %v", err))
	}

	// Detect content type; if it's an image, return as image block
	mimeType := http.DetectContentType(data)
	if imageTypes[mimeType] {
		encoded := base64.StdEncoding.EncodeToString(data)
		return CallToolResult{
			Content: []ToolContent{{Type: "image", Data: encoded, MimeType: mimeType}},
		}
	}

	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: string(data)}},
	}
}

func toolFileWrite(args map[string]any) CallToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return errorResult("missing or invalid 'path' argument")
	}
	content, ok := args["content"].(string)
	if !ok {
		return errorResult("missing or invalid 'content' argument")
	}

	path = resolvePath(path)

	// ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return errorResult(fmt.Sprintf("mkdir: %v", err))
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return errorResult(fmt.Sprintf("write file: %v", err))
	}

	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}},
	}
}

func toolFileList(args map[string]any) CallToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return errorResult("missing or invalid 'path' argument")
	}

	path = resolvePath(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return errorResult(fmt.Sprintf("list dir: %v", err))
	}

	var lines []string
	for _, e := range entries {
		suffix := ""
		if e.IsDir() {
			suffix = "/"
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		lines = append(lines, fmt.Sprintf("%s%s\t%d bytes", e.Name(), suffix, size))
	}

	if len(lines) == 0 {
		return CallToolResult{
			Content: []ToolContent{{Type: "text", Text: "(empty directory)"}},
		}
	}

	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: strings.Join(lines, "\n")}},
	}
}

// resolvePath converts relative paths to absolute paths under /workspace
func resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join("/workspace", path)
}

func errorResult(msg string) CallToolResult {
	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
