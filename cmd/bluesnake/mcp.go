package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/internal/mcp"
	"github.com/agentberlin/bluesnake/internal/tunnel"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	var storeDir, addr string
	var public, tunnelInsecure bool
	var tunnelServerName string
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

With --public, bluesnake also opens a reverse tunnel and prints a public HTTPS
URL (https://<id>.t.snake.blue/<token>/mcp) that proxies to this local server,
so a remote MCP client can reach it without any port-forwarding. The tunnel
identity is stored under the crawl directory and reused across runs, so the URL
is stable. Without --public the server stays bound to localhost only.

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

			if public {
				if err := startPublicTunnel(ctx, cmd, storeDir, ln.Addr().String(), tunnelInsecure, tunnelServerName); err != nil {
					return exitErr{1, fmt.Errorf("starting public tunnel: %w", err)}
				}
			}

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
	cmd.Flags().BoolVar(&public, "public", false, "expose a public HTTPS URL via the bluesnake reverse tunnel")
	cmd.Flags().BoolVar(&tunnelInsecure, "tunnel-insecure", false, "skip tunnel server TLS verification (dev only)")
	cmd.Flags().StringVar(&tunnelServerName, "tunnel-server-name", "", "override tunnel server TLS name (dev only)")
	_ = cmd.Flags().MarkHidden("tunnel-insecure")
	_ = cmd.Flags().MarkHidden("tunnel-server-name")
	return cmd
}

// startPublicTunnel registers (or reuses) the tunnel identity and brings the
// reverse tunnel up in the background, forwarding to the local MCP listener.
func startPublicTunnel(ctx context.Context, cmd *cobra.Command, storeDir, localAddr string, insecure bool, serverName string) error {
	out := cmd.OutOrStdout()
	regCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, err := tunnel.EnsureIdentity(regCtx, storeDir)
	if err != nil {
		return err
	}

	c := tunnel.New(tunnel.Config{
		Identity:           id,
		LocalAddr:          localAddr,
		LogDir:             filepath.Join(storeDir, "logs"),
		InsecureSkipVerify: insecure,
		ServerName:         serverName,
		OnStatus: func(s tunnel.Status) {
			switch s.State {
			case tunnel.StateOnline:
				fmt.Fprintf(out, "public tunnel online: %s\n", s.MCPURL)
			case tunnel.StateError:
				fmt.Fprintf(cmd.ErrOrStderr(), "public tunnel reconnecting: %s\n", s.Err)
			}
		},
	})
	fmt.Fprintf(out, "public MCP URL: %s\n", id.MCPURL())
	go func() { _ = c.Run(ctx) }()
	return nil
}
