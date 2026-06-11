package main

import (
	"fmt"
	"net"
	"net/http"

	"github.com/agentberlin/bluesnake/internal/serve"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var storeDir, addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a read-only JSON API over the stored crawls",
		Long: `Serve exposes every stored crawl over a localhost JSON API:
  /api/crawls
  /api/crawls/{id}/overview
  /api/crawls/{id}/datasets[/{name}?issue=<issue_id>]
  /api/crawls/{id}/reports[/{name}]
  /api/crawls/{id}/issues
  /api/crawls/{id}/page?url=<url>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return exitErr{2, err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "serving crawl API on http://%s/api/crawls\n", ln.Addr())
			return http.Serve(ln, serve.Handler(storeDir))
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8472", "listen address")
	return cmd
}
