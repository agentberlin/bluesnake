package main

import (
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/agentberlin/bluesnake/internal/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	var storeDir, addr string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server so LLM agents can drive the crawler",
		Long: `Run a Model Context Protocol server exposing bluesnake to LLM agents:
starting and controlling crawls (with every config knob), and read-only SQL
over the stored crawl databases.

The server speaks the MCP streamable-HTTP transport on localhost — the same
endpoint the desktop app's MCP toggle exposes:

  bluesnake mcp
  claude mcp add --transport http bluesnake http://127.0.0.1:8473/mcp

or in a client's JSON config:

  {"mcpServers": {"bluesnake": {"type": "http", "url": "http://127.0.0.1:8473/mcp"}}}

A crawl in flight when the server shuts down is paused, not lost — it shows
up as interrupted and can be resumed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := mcp.NewRunner(storeDir)
			defer runner.Shutdown() // pause any live crawl, resumable
			srv := mcp.NewServer(runner, appVersion)

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return exitErr{2, err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "bluesnake MCP server on http://%s/mcp\n", ln.Addr())
			hs := &http.Server{Handler: srv.HTTPHandler()}
			go func() {
				<-ctx.Done()
				hs.Close()
			}()
			if err := hs.Serve(ln); err != http.ErrServerClosed {
				return exitErr{1, err}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8473", "listen address")
	return cmd
}
