package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/seaturt/server/internal/agent"
	"github.com/gin-gonic/gin"
)

// Server is the HTTP API server.
type Server struct {
	engine *gin.Engine
	port   int
}

// NewServer creates and configures the HTTP server with all routes registered.
func NewServer(port int, mgr *agent.Manager, maxImageSize int) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())
	engine.Use(logMiddleware())

	agentHandler := NewAgentHandler(mgr)
	chatHandler := NewChatHandler(mgr, maxImageSize)
	fileHandler := NewFileHandler(mgr)

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

			// Chat
			agents.POST("/:id/chat", chatHandler.Chat)
			agents.GET("/:id/history", chatHandler.GetHistory)
			agents.DELETE("/:id/history", chatHandler.DeleteHistory)

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
