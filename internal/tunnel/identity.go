// Package tunnel embeds the reverse-tunnel client: it gives a running local
// MCP server a stable public HTTPS URL by holding an outbound multiplexed
// connection to the bluesnake tunnel server (see tunnelserver/ and
// docs/TUNNEL.md).
//
// One credential exists per install: the connect secret authenticates the
// tunnel connection and never leaves this machine. The public URL itself is an
// unguessable random subdomain; in phase 1 that randomness is what keeps it
// private (no per-request token).
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	// DefaultAPIBase is the production control plane. BLUESNAKE_TUNNEL_API
	// overrides it for development against a local tunnel server.
	DefaultAPIBase = "https://api.snake.blue"

	envAPIBase   = "BLUESNAKE_TUNNEL_API"
	identityFile = "tunnel.json"
)

// Identity is one install's tunnel identity, persisted (0600) at
// <storeDir>/tunnel.json and shared by the CLI and the desktop app so both
// surface the same public URL. Losing the file means registering a fresh
// subdomain — accepted for phase 1.
type Identity struct {
	TunnelID      string `json:"tunnel_id"`
	ConnectSecret string `json:"connect_secret"`
	PublicHost    string `json:"public_host"`  // e.g. k3x9qzpw04ab.t.snake.blue
	ConnectAddr   string `json:"connect_addr"` // e.g. connect.t.snake.blue:443
	APIBase       string `json:"api_base"`
}

// APIBase returns the control-plane base URL, honouring the env override.
func APIBase() string {
	if v := os.Getenv(envAPIBase); v != "" {
		return v
	}
	return DefaultAPIBase
}

// IdentityPath returns where the identity lives under a store directory.
func IdentityPath(storeDir string) string { return filepath.Join(storeDir, identityFile) }

// MCPURL is the URL MCP clients are configured with.
func (id *Identity) MCPURL() string {
	return fmt.Sprintf("https://%s/mcp", id.PublicHost)
}

// PublicURL is the bare public origin for display.
func (id *Identity) PublicURL() string { return "https://" + id.PublicHost }

func (id *Identity) valid() bool {
	return id.TunnelID != "" && id.ConnectSecret != "" &&
		id.PublicHost != "" && id.ConnectAddr != ""
}

// LoadIdentity reads a previously saved identity. It returns (nil, nil) when
// none exists yet.
func LoadIdentity(storeDir string) (*Identity, error) {
	data, err := os.ReadFile(IdentityPath(storeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil || !id.valid() {
		return nil, fmt.Errorf("corrupt tunnel identity %s — delete it to register a new public URL", IdentityPath(storeDir))
	}
	return &id, nil
}

func saveIdentity(storeDir string, id *Identity) error {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(IdentityPath(storeDir), data, 0o600)
}

// EnsureIdentity loads the install's identity, registering with the control
// plane on first use.
func EnsureIdentity(ctx context.Context, storeDir string) (*Identity, error) {
	id, err := LoadIdentity(storeDir)
	if err != nil || id != nil {
		return id, err
	}
	id, err = Register(ctx, APIBase())
	if err != nil {
		return nil, err
	}
	if err := saveIdentity(storeDir, id); err != nil {
		return nil, err
	}
	return id, nil
}

// Register asks the control plane for a fresh identity. The connect secret in
// the response is shown exactly once by the server; the caller must persist it.
func Register(ctx context.Context, apiBase string) (*Identity, error) {
	var resp struct {
		TunnelID      string `json:"tunnel_id"`
		ConnectSecret string `json:"connect_secret"`
		PublicHost    string `json:"public_host"`
		ConnectAddr   string `json:"connect_addr"`
	}
	if err := postJSON(ctx, apiBase+"/v1/register", struct{}{}, &resp); err != nil {
		return nil, fmt.Errorf("registering tunnel: %w", err)
	}
	id := &Identity{
		TunnelID:      resp.TunnelID,
		ConnectSecret: resp.ConnectSecret,
		PublicHost:    resp.PublicHost,
		ConnectAddr:   resp.ConnectAddr,
		APIBase:       apiBase,
	}
	if !id.valid() {
		return nil, fmt.Errorf("registering tunnel: incomplete response from control plane")
	}
	return id, nil
}

func postJSON(ctx context.Context, url string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("control plane returned %s", resp.Status)
	}
	return json.Unmarshal(data, out)
}
