package mcp

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "bluesnake"
	ServerVersion = "1.0.0"
)

// MCPServer wraps the core BlueSnake app and exposes it via MCP protocol
type MCPServer struct {
	server *mcp.Server
	app    *app.App
	store  *store.Store
	ctx    context.Context
	logger *log.Logger
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(ctx context.Context) (*MCPServer, error) {
	logger := log.New(os.Stderr, "[BlueSnake MCP] ", log.LstdFlags)

	// Initialize database store (uses default ~/.bluesnake/bluesnake.db)
	logger.Printf("Initializing database...")
	st, err := store.NewStore()
	if err != nil {
		return nil, err
	}

	// Create core app with NoOp emitter (MCP doesn't need event notifications yet)
	emitter := &app.NoOpEmitter{}
	coreApp := app.NewApp(st, emitter)
	coreApp.Startup(ctx)

	// Create MCP server
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, nil)

	s := &MCPServer{
		server: mcpServer,
		app:    coreApp,
		store:  st,
		ctx:    ctx,
		logger: logger,
	}

	// Register all tools, resources, and prompts
	s.registerTools()

	logger.Printf("MCP server initialized successfully")
	return s, nil
}

// GetServer returns the internal MCP server instance
func (s *MCPServer) GetServer() *mcp.Server {
	return s.server
}

// RunHTTP starts the MCP server with HTTP transport using StreamableHTTPHandler
func (s *MCPServer) RunHTTP(addr string) (*http.Server, error) {
	s.logger.Printf("Starting MCP HTTP server on %s...", addr)

	// Create StreamableHTTPHandler
	handler := mcp.NewStreamableHTTPHandler(
		func(req *http.Request) *mcp.Server {
			return s.server
		},
		nil, // Use default StreamableHTTPOptions
	)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Start server in goroutine
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("HTTP server error: %v", err)
		}
	}()

	s.logger.Printf("MCP HTTP server started successfully on %s", addr)
	return httpServer, nil
}

// Close performs cleanup
func (s *MCPServer) Close() error {
	s.logger.Printf("Shutting down MCP server...")
	// Store doesn't have a Close method - GORM manages connections automatically
	return nil
}
