package render

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// chromeProxy resolves what Chrome's --proxy-server is set to. It needs no
// Chrome, so it is unit-testable even though the renderer around it is not —
// and it carries the whole of D6 (renders must use the same egress as the raw
// fetch), so leaving it to a Chrome-tagged test would leave the guarantee
// unverified on every machine that runs the default suite.

func TestChromeProxyWithoutConfiguredProxy(t *testing.T) {
	f, arg, err := chromeProxy(config.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Error("started a forwarder for an unproxied crawl")
	}
	if arg != "" {
		t.Errorf("--proxy-server = %q, want empty for an unproxied crawl", arg)
	}
}

// A credential-free proxy goes straight to Chrome: no shim needed, so none is
// started.
func TestChromeProxyPassesCredentialFreeProxyDirectly(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxy = "http://proxy.example:8080"

	f, arg, err := chromeProxy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Error("started a forwarder for a proxy that needs none")
	}
	if arg != "http://proxy.example:8080" {
		t.Errorf("--proxy-server = %q", arg)
	}
}

// Chrome rejects user:pass@host, so a credentialed proxy must arrive as a
// loopback address with the credentials stripped — the forwarder adds them on
// the way out.
func TestChromeProxyStartsForwarderForCredentialedProxy(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxy = "http://cust-zone:sekrit@brd.superproxy.io:44445"

	f, arg, err := chromeProxy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("no forwarder started — Chrome would be handed credentials it cannot use")
	}
	t.Cleanup(func() { _ = f.Close() })

	if !strings.HasPrefix(arg, "http://127.0.0.1:") {
		t.Errorf("--proxy-server = %q, want a loopback address", arg)
	}
	for _, secret := range []string{"sekrit", "cust-zone", "@"} {
		if strings.Contains(arg, secret) {
			t.Errorf("--proxy-server %q leaks %q", arg, secret)
		}
	}
	if arg != f.ProxyServer() {
		t.Errorf("--proxy-server %q does not match the forwarder's address %q", arg, f.ProxyServer())
	}
}

// SOCKS carries no header to inject credentials into. Failing loudly at
// construction is the point: a browser that quietly went direct would reopen
// the origin-IP leak the whole feature exists to close (D6).
func TestChromeProxyRefusesCredentialedSOCKS(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxy = "socks5://user:sekrit@socks.example:1080"

	f, arg, err := chromeProxy(cfg)
	if err == nil {
		if f != nil {
			_ = f.Close()
		}
		t.Fatalf("credentialed SOCKS accepted (arg %q) — Chrome would render direct", arg)
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error = %v, want it to explain the credential limitation", err)
	}
	if strings.Contains(err.Error(), "sekrit") {
		t.Errorf("error message leaks the password: %v", err)
	}
}

// An unauthenticated SOCKS proxy is fine — Chrome speaks it directly.
func TestChromeProxyAcceptsUnauthenticatedSOCKS(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxy = "socks5://socks.example:1080"

	f, arg, err := chromeProxy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		_ = f.Close()
		t.Error("started a forwarder for an unauthenticated SOCKS proxy")
	}
	if arg != "socks5://socks.example:1080" {
		t.Errorf("--proxy-server = %q", arg)
	}
}

// include_direct puts an unproxied egress in the rotation for FETCHES, but
// Chrome still has to be pointed somewhere: it must pick the real proxy, not
// the direct entry.
func TestChromeProxySkipsTheDirectEntry(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxies = []config.ProxyEntry{{URL: "http://proxy.example:8080"}}
	cfg.HTTP.ProxyIncludeDirect = true

	f, arg, err := chromeProxy(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		_ = f.Close()
	}
	if arg != "http://proxy.example:8080" {
		t.Errorf("--proxy-server = %q, want the real proxy rather than the direct entry", arg)
	}
}

// A misconfigured pool must fail here rather than silently launching an
// unproxied browser.
func TestChromeProxyPropagatesPoolErrors(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Proxies = []config.ProxyEntry{{URL: "http://p:8080", PasswordEnv: "BLUESNAKE_RENDER_PW_UNSET"}}

	f, _, err := chromeProxy(cfg)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("an unresolvable password_env produced no error")
	}
	if !strings.Contains(err.Error(), "BLUESNAKE_RENDER_PW_UNSET") {
		t.Errorf("error = %v, want it to name the missing variable", err)
	}
}
