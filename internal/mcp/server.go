package mcp

import (
	"context"
	"log"
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

// Run starts the MCP server with stdio transport
func (s *MCPServer) Run() error {
	s.logger.Printf("Starting MCP server on stdio transport...")
	return s.server.Run(s.ctx, &mcp.StdioTransport{})
}

// Close performs cleanup
func (s *MCPServer) Close() error {
	s.logger.Printf("Shutting down MCP server...")
	// Store doesn't have a Close method - GORM manages connections automatically
	return nil
}
