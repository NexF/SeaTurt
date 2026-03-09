package api

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/seaturt/server/internal/agent"
	cronpkg "github.com/seaturt/server/internal/cron"
	"github.com/gin-gonic/gin"
)

// Server is the HTTP API server.
type Server struct {
	engine *gin.Engine
	port   int
}

// NewServer creates and configures the HTTP server with all routes registered.
// webFS is an optional embedded filesystem containing the frontend build output (dist/).
// When nil, only the API is served (development mode).
func NewServer(port int, mgr *agent.Manager, maxImageSize int, webFS fs.FS, scheduler ...*cronpkg.Scheduler) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())
	engine.Use(logMiddleware())

	agentHandler := NewAgentHandler(mgr)
	chatHandler := NewChatHandler(mgr, maxImageSize)
	fileHandler := NewFileHandler(mgr)
	sessionHandler := NewSessionHandler(mgr)

	// CronJob handler (optional scheduler for testing without it)
	var sched *cronpkg.Scheduler
	if len(scheduler) > 0 {
		sched = scheduler[0]
	}
	cronJobHandler := NewCronJobHandler(mgr, sched)

	api := engine.Group("/api")
	{
		// Models
		api.GET("/models", agentHandler.ListModels)

		// Agent management
		agents := api.Group("/agents")
		{
			agents.POST("", agentHandler.CreateAgent)
			agents.GET("", agentHandler.ListAgents)
			agents.GET("/:id", agentHandler.GetAgent)
			agents.POST("/:id/start", agentHandler.StartAgent)
			agents.POST("/:id/stop", agentHandler.StopAgent)
			agents.DELETE("/:id", agentHandler.DeleteAgent)

			// Ports
			agents.GET("/:id/ports", agentHandler.GetPorts)

			// System Prompt
			agents.GET("/:id/system-prompt", agentHandler.GetSystemPrompt)
			agents.PUT("/:id/system-prompt", agentHandler.UpdateSystemPrompt)

			// Desktop
			agents.GET("/:id/desktop", agentHandler.GetDesktop)

			// Sessions
			agents.GET("/:id/sessions", sessionHandler.ListSessions)
			agents.POST("/:id/sessions", sessionHandler.CreateSession)
			agents.PUT("/:id/sessions/:sid", sessionHandler.UpdateSession)
			agents.DELETE("/:id/sessions/:sid", sessionHandler.DeleteSession)

			// CronJobs (v0.3.0)
			agents.GET("/:id/cron-jobs", cronJobHandler.ListCronJobs)
			agents.POST("/:id/cron-jobs", cronJobHandler.CreateCronJob)
			agents.GET("/:id/cron-jobs/:jid", cronJobHandler.GetCronJob)
			agents.PUT("/:id/cron-jobs/:jid", cronJobHandler.UpdateCronJob)
			agents.DELETE("/:id/cron-jobs/:jid", cronJobHandler.DeleteCronJob)
			agents.POST("/:id/cron-jobs/:jid/trigger", cronJobHandler.TriggerCronJob)
			agents.GET("/:id/cron-jobs/:jid/history", cronJobHandler.ListCronJobHistory)

			// Chat (session-level)
			agents.POST("/:id/sessions/:sid/chat", chatHandler.Chat)
			agents.POST("/:id/sessions/:sid/chat/cancel", chatHandler.CancelChat)
			agents.POST("/:id/sessions/:sid/chat/cancel-tool/:toolCallId", chatHandler.CancelToolCall)
			agents.GET("/:id/sessions/:sid/history", chatHandler.GetHistory)
			agents.DELETE("/:id/sessions/:sid/history", chatHandler.DeleteHistory)

			// Workspace files
			agents.GET("/:id/files", fileHandler.ListFiles)
			agents.GET("/:id/files/*filepath", fileHandler.GetFile)
			agents.POST("/:id/files", fileHandler.UploadFile)
		}
	}

	// Health check
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Serve embedded frontend in production mode
	if webFS != nil {
		setupStaticFiles(engine, webFS)
		slog.Info("static file serving enabled (embed.FS)")
	}

	return &Server{engine: engine, port: port}
}

// Run starts the HTTP server (blocking).
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("HTTP server starting", "addr", addr)
	return s.engine.Run(addr)
}

// ServeHTTP implements http.Handler, allowing Server to be used with httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.engine.ServeHTTP(w, req)
}

// Handler returns the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.engine
}

// logMiddleware returns a Gin middleware that logs requests using slog.
func logMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := c.Writer.Header()
		path := c.Request.URL.Path

		c.Next()

		_ = start
		slog.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"ip", c.ClientIP(),
		)
	}
}

// corsMiddleware returns a Gin middleware that handles CORS for frontend access.
// It allows requests from any localhost origin (for development) and handles preflight requests.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Allow localhost origins (any port) for development
		if origin != "" && isAllowedOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Max-Age", "86400")
		}

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// isAllowedOrigin checks if the origin is an allowed localhost origin.
func isAllowedOrigin(origin string) bool {
	// Allow any localhost/127.0.0.1 origin (development)
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "https://localhost") ||
		strings.HasPrefix(origin, "https://127.0.0.1")
}

// setupStaticFiles configures the Gin engine to serve embedded frontend files.
// It serves /assets/* directly and falls back to index.html for SPA routing.
func setupStaticFiles(engine *gin.Engine, webFS fs.FS) {
	// Serve /assets/* — sub into the "assets" subdirectory of the embedded FS
	if assetsFS, err := fs.Sub(webFS, "assets"); err == nil {
		engine.StaticFS("/assets", http.FS(assetsFS))
	}

	// Pre-read index.html for SPA fallback
	indexHTML, _ := fs.ReadFile(webFS, "index.html")

	// All other non-API routes: try exact file, then fallback to index.html (SPA)
	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// Don't serve index.html for API routes
		if strings.HasPrefix(path, "/api") {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}

		// Try to serve the exact file (e.g. /vite.svg, /favicon.ico)
		if path != "/" {
			name := strings.TrimPrefix(path, "/")
			if f, err := webFS.Open(name); err == nil {
				defer f.Close()
				ct := mime.TypeByExtension(filepath.Ext(name))
				if ct == "" {
					ct = "application/octet-stream"
				}
				c.Header("Content-Type", ct)
				c.Status(http.StatusOK)
				io.Copy(c.Writer, f)
				return
			}
		}

		// Fallback to index.html for SPA routing
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
}
