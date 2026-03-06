package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/seaturt/server/internal/agent"
	"github.com/gin-gonic/gin"
)

// FileHandler handles workspace file API endpoints.
type FileHandler struct {
	mgr *agent.Manager
}

// NewFileHandler creates a new FileHandler.
func NewFileHandler(mgr *agent.Manager) *FileHandler {
	return &FileHandler{mgr: mgr}
}

// FileEntry represents a file or directory entry in the API response.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ListFiles handles GET /api/agents/:id/files — returns workspace directory listing.
// Query parameter: path (optional, relative path within workspace, default: "")
func (h *FileHandler) ListFiles(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	relPath := c.Query("path")
	dirPath := filepath.Join(ag.WorkspacePath, relPath)

	// Security: ensure the resolved path is within the workspace
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	absWorkspace, _ := filepath.Abs(ag.WorkspacePath)
	if !strings.HasPrefix(absDir, absWorkspace) {
		c.JSON(http.StatusForbidden, gin.H{"error": "path traversal not allowed"})
		return
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	files := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryRelPath := filepath.Join(relPath, entry.Name())
		files = append(files, FileEntry{
			Name:  entry.Name(),
			Path:  entryRelPath,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
}

// GetFile handles GET /api/agents/:id/files/*filepath — reads a file from the workspace.
func (h *FileHandler) GetFile(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	relPath := c.Param("filepath")
	// Gin captures the leading slash in wildcard
	relPath = strings.TrimPrefix(relPath, "/")

	if relPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filepath is required"})
		return
	}

	filePath := filepath.Join(ag.WorkspacePath, relPath)

	// Security: ensure the resolved path is within the workspace
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	absWorkspace, _ := filepath.Abs(ag.WorkspacePath)
	if !strings.HasPrefix(absFile, absWorkspace) {
		c.JSON(http.StatusForbidden, gin.H{"error": "path traversal not allowed"})
		return
	}

	info, err := os.Stat(absFile)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory, use GET /files?path= for directory listing"})
		return
	}

	// Detect content type
	contentType := detectContentType(absFile)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", info.Name()))
	c.File(absFile)
}

// UploadFile handles POST /api/agents/:id/files — uploads a file to the workspace.
// Accepts multipart/form-data with:
//   - "file": the file to upload
//   - "path": destination relative path within workspace (optional, default: root)
func (h *FileHandler) UploadFile(c *gin.Context) {
	id := c.Param("id")

	ag, err := h.mgr.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file field is required"})
		return
	}
	defer file.Close()

	// Destination path
	destDir := c.PostForm("path")
	if destDir == "" {
		destDir = "."
	}

	destPath := filepath.Join(ag.WorkspacePath, destDir, header.Filename)

	// Security: ensure the resolved path is within the workspace
	absDest, err := filepath.Abs(destPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	absWorkspace, _ := filepath.Abs(ag.WorkspacePath)
	if !strings.HasPrefix(absDest, absWorkspace) {
		c.JSON(http.StatusForbidden, gin.H{"error": "path traversal not allowed"})
		return
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(absDest), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create directory"})
		return
	}

	// Write file
	dst, err := os.Create(absDest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create file"})
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file"})
		return
	}

	// Return relative path within workspace
	relResult, _ := filepath.Rel(ag.WorkspacePath, absDest)

	c.JSON(http.StatusCreated, gin.H{
		"path": relResult,
		"size": written,
		"name": header.Filename,
	})
}

// detectContentType returns a MIME type for the given file path.
func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	mimeTypes := map[string]string{
		".md":   "text/markdown; charset=utf-8",
		".txt":  "text/plain; charset=utf-8",
		".html": "text/html; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".js":   "application/javascript; charset=utf-8",
		".json": "application/json; charset=utf-8",
		".csv":  "text/csv; charset=utf-8",
		".xml":  "application/xml; charset=utf-8",
		".yaml": "text/yaml; charset=utf-8",
		".yml":  "text/yaml; charset=utf-8",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".pdf":  "application/pdf",
		".go":   "text/plain; charset=utf-8",
		".py":   "text/plain; charset=utf-8",
		".rs":   "text/plain; charset=utf-8",
		".ts":   "text/plain; charset=utf-8",
		".tsx":  "text/plain; charset=utf-8",
		".jsx":  "text/plain; charset=utf-8",
		".sh":   "text/plain; charset=utf-8",
		".toml": "text/plain; charset=utf-8",
		".log":  "text/plain; charset=utf-8",
	}

	if ct, ok := mimeTypes[ext]; ok {
		return ct
	}

	// Try to detect from file content
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}
