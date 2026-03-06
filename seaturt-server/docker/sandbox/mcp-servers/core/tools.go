package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// defaultShellTimeout is the default timeout for shell commands (120 seconds).
	defaultShellTimeout = 120 * time.Second

	// maxShellTimeout is the maximum allowed timeout (30 minutes).
	maxShellTimeout = 30 * time.Minute
)

func toolShellExec(args map[string]any) CallToolResult {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return errorResult("missing or invalid 'command' argument")
	}

	// Check if background mode is requested
	if bg, ok := args["background"].(bool); ok && bg {
		return shellExecBackground(command)
	}

	// Parse optional timeout (seconds)
	timeout := defaultShellTimeout
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
		if timeout > maxShellTimeout {
			timeout = maxShellTimeout
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = "/workspace"
	// Use a new process group so we can kill the entire tree on timeout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine output
	var text string
	if stderr.Len() > 0 {
		text = stdout.String() + stderr.String()
	} else {
		text = stdout.String()
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// Kill the process group to clean up any children
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			text += fmt.Sprintf("\n\n[TIMEOUT] Command exceeded %v timeout and was killed. "+
				"Use background=true for long-running processes like servers or browsers.",
				timeout)
		} else {
			text += fmt.Sprintf("\nexit error: %v", err)
		}
		return CallToolResult{
			Content: []ToolContent{{Type: "text", Text: text}},
			IsError: true,
		}
	}

	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

// shellExecBackground starts a command in the background using nohup + setsid,
// returning immediately with the PID. Output is redirected to /tmp/bg_<pid>.log.
func shellExecBackground(command string) CallToolResult {
	// Use setsid to fully detach; redirect output to a log file
	// The wrapper script: setsid sh -c '<command>' > /tmp/bg_$$.log 2>&1 &
	wrapper := fmt.Sprintf(
		`nohup setsid sh -c '%s' > /tmp/bg_cmd.log 2>&1 & echo $!`,
		strings.ReplaceAll(command, "'", "'\"'\"'"),
	)

	cmd := exec.Command("sh", "-c", wrapper)
	cmd.Dir = "/workspace"
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("failed to start background process: %v\n%s", err, string(output)))
	}

	pid := strings.TrimSpace(string(output))
	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: fmt.Sprintf(
			"Process started in background (PID: %s)\nOutput log: /tmp/bg_cmd.log\n"+
				"Use `cat /tmp/bg_cmd.log` to check output, or `kill %s` to stop.",
			pid, pid,
		)}},
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
