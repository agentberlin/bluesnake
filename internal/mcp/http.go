package mcp

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPHandler serves the MCP streamable-HTTP transport at /mcp: the client
// POSTs one JSON-RPC message and gets the reply as application/json (a
// stateless server needs neither SSE streams nor session ids — both are
// optional in the spec). GET (server-initiated stream) and DELETE (session
// teardown) are answered 405 as the spec directs for servers that don't
// offer them.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		resp := s.Handle(r.Context(), body)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted) // notification: no reply due
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	})
	return mux
}

// originAllowed rejects cross-origin browser calls (DNS-rebinding hygiene
// per the MCP spec). Non-browser MCP clients send no Origin header.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
