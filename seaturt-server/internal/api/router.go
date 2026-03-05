package api

import (
	"fmt"
	"log/slog"
	"net/http"

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
	engine.Use(logMiddleware())

	agentHandler := NewAgentHandler(mgr)
	chatHandler := NewChatHandler(mgr, maxImageSize)

	api := engine.Group("/api")
	{
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

			// Chat
			agents.POST("/:id/chat", chatHandler.Chat)
			agents.GET("/:id/history", chatHandler.GetHistory)
			agents.DELETE("/:id/history", chatHandler.DeleteHistory)
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
