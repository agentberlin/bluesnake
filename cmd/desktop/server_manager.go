// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/types"
)

const (
	// DEFAULT_START_PORT is the starting port for port retry logic
	DEFAULT_START_PORT = 38888

	// MAX_PORT_ATTEMPTS is the number of ports to try before giving up
	MAX_PORT_ATTEMPTS = 10
)

// ServerManager manages the HTTP server lifecycle
type ServerManager struct {
	httpServer     *http.Server
	app            *app.App
	ctx            context.Context
	isRunning      bool
	localURL       string
	port           int
	lastError      string
	mu             sync.RWMutex
	serverShutdown chan struct{}
}

// NewServerManager creates a new ServerManager instance
func NewServerManager(ctx context.Context, app *app.App) *ServerManager {
	return &ServerManager{
		app:            app,
		ctx:            ctx,
		serverShutdown: make(chan struct{}),
	}
}

// StartWithTunnel starts the HTTP server on localhost
// It tries ports starting from 38888 and returns the local URL
func (sm *ServerManager) StartWithTunnel() (*types.ServerInfo, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.isRunning {
		return &types.ServerInfo{
			PublicURL: sm.localURL,
			Port:      sm.port,
		}, nil
	}

	// Try to start server on available port
	var lastErr error
	for i := 0; i < MAX_PORT_ATTEMPTS; i++ {
		port := DEFAULT_START_PORT + i

		// Try to start HTTP server on this port
		if err := sm.startHTTPServer(port); err != nil {
			lastErr = err
			continue // Try next port
		}

		// Server started successfully
		localURL := fmt.Sprintf("http://localhost:%d", port)
		sm.isRunning = true
		sm.localURL = localURL
		sm.port = port
		sm.lastError = ""

		return &types.ServerInfo{
			PublicURL: localURL,
			Port:      port,
		}, nil
	}

	// All ports failed
	sm.lastError = fmt.Sprintf("No available ports found: %v", lastErr)
	return nil, fmt.Errorf("failed to start server after %d attempts: %w", MAX_PORT_ATTEMPTS, lastErr)
}

// Stop stops the HTTP server
func (sm *ServerManager) Stop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.isRunning {
		return fmt.Errorf("server is not running")
	}

	// Stop HTTP server
	if err := sm.stopHTTPServer(); err != nil {
		sm.lastError = fmt.Sprintf("Failed to stop server: %v", err)
		return err
	}

	sm.isRunning = false
	sm.localURL = ""
	sm.port = 0
	sm.lastError = ""

	return nil
}

// GetStatus returns the current server status
func (sm *ServerManager) GetStatus() *types.ServerStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return &types.ServerStatus{
		IsRunning: sm.isRunning,
		PublicURL: sm.localURL,
		Port:      sm.port,
		Error:     sm.lastError,
	}
}

// startHTTPServer starts the HTTP server on the specified port
func (sm *ServerManager) startHTTPServer(port int) error {
	// Check if port is available
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d is not available: %w", port, err)
	}
	listener.Close() // Close immediately, we just wanted to check availability

	// Create HTTP server (reusing the server logic from cmd/server)
	// We'll import and use the Server type from cmd/server
	server := NewServer(sm.app)

	sm.httpServer = &http.Server{
		Addr:         addr,
		Handler:      server,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background
	go func() {
		fmt.Printf("Starting HTTP server on %s\n", addr)
		if err := sm.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sm.mu.Lock()
			sm.lastError = fmt.Sprintf("Server error: %v", err)
			sm.isRunning = false
			sm.mu.Unlock()
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Wait for server to be ready (up to 2 seconds)
	serverReady := false
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		// Try to connect to the server
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/health", port))
		if err == nil {
			resp.Body.Close()
			serverReady = true
			fmt.Printf("HTTP server is ready on port %d\n", port)
			break
		}
	}

	if !serverReady {
		return fmt.Errorf("server did not become ready in time")
	}

	return nil
}

// stopHTTPServer gracefully stops the HTTP server
func (sm *ServerManager) stopHTTPServer() error {
	if sm.httpServer == nil {
		return nil
	}

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sm.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	sm.httpServer = nil
	return nil
}
