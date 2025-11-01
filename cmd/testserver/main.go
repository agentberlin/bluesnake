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
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/server"
	"github.com/agentberlin/bluesnake/internal/store"
)

func main() {
	// Parse command-line flags
	port := flag.Int("port", 8080, "Port to run the HTTP server on")
	host := flag.String("host", "0.0.0.0", "Host to bind the HTTP server to")
	flag.Parse()

	// Initialize the database store
	st, err := store.NewStore()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Create core app with NoOpEmitter (no events for HTTP server yet)
	coreApp := app.NewApp(st, &app.NoOpEmitter{})
	coreApp.Startup(context.Background())

	// Create HTTP server
	srv := server.NewServer(coreApp)

	// Create HTTP server with graceful shutdown
	addr := fmt.Sprintf("%s:%d", *host, *port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting Blue Snake HTTP server on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
