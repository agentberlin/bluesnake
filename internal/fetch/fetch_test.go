package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
)

func newClient(t *testing.T, mutate func(*config.Config), opts ...Option) *Client {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	c, err := New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBasicFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Last-Modified", "Tue, 10 Jun 2026 10:00:00 GMT")
		fmt.Fprint(w, "<html>hi</html>")
	}))
	defer srv.Close()

	res := newClient(t, nil).Fetch(context.Background(), srv.URL+"/page")
	if res.FetchError != "" {
		t.Fatalf("unexpected error: %s", res.FetchError)
	}
	if res.StatusCode != 200 || res.Status != "OK" {
		t.Errorf("status = %d %q", res.StatusCode, res.Status)
	}
	if string(res.Body) != "<html>hi</html>" {
		t.Errorf("body = %q", res.Body)
	}
	if res.ContentType != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", res.ContentType)
	}
	if res.Headers.Get("Last-Modified") == "" {
		t.Error("response headers must be captured")
	}
	if res.HTTPVersion == "" {
		t.Error("http version must be recorded")
	}
	if res.ResponseTimeMs < 0 {
		t.Error("response time must be recorded")
	}
}

func TestRedirectsAreData(t *testing.T) {
	mux := http.NewServeMux()
	var hitNew atomic.Int32
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/rel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "next")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) { hitNew.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newClient(t, nil)

	res := c.Fetch(context.Background(), srv.URL+"/old")
	if res.StatusCode != 301 {
		t.Fatalf("status = %d, want 301", res.StatusCode)
	}
	if res.RedirectURL != srv.URL+"/new" {
		t.Errorf("redirect url = %q, want %q", res.RedirectURL, srv.URL+"/new")
	}
	if res.RedirectType != "http" {
		t.Errorf("redirect type = %q", res.RedirectType)
	}
	if hitNew.Load() != 0 {
		t.Error("redirect target must not be fetched")
	}

	// relative Location resolves against the request URL
	res = c.Fetch(context.Background(), srv.URL+"/rel")
	if want := srv.URL + "/next"; res.RedirectURL != want {
		t.Errorf("relative redirect = %q, want %q", res.RedirectURL, want)
	}
}

func TestRetry5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	t.Run("no retries by default", func(t *testing.T) {
		calls.Store(0)
		res := newClient(t, nil).Fetch(context.Background(), srv.URL)
		if res.StatusCode != 503 || calls.Load() != 1 {
			t.Errorf("status %d after %d calls, want 503 after 1", res.StatusCode, calls.Load())
		}
	})

	t.Run("retries until success", func(t *testing.T) {
		calls.Store(0)
		res := newClient(t, func(c *config.Config) { c.Advanced.Retry5xx = 5 }).Fetch(context.Background(), srv.URL)
		if res.StatusCode != 200 {
			t.Errorf("status = %d, want 200", res.StatusCode)
		}
		if calls.Load() != 3 {
			t.Errorf("calls = %d, want 3 (stop on first success)", calls.Load())
		}
	})

	t.Run("gives up after configured retries", func(t *testing.T) {
		calls.Store(0)
		res := newClient(t, func(c *config.Config) { c.Advanced.Retry5xx = 1 }).Fetch(context.Background(), srv.URL)
		if res.StatusCode != 503 || calls.Load() != 2 {
			t.Errorf("status %d after %d calls, want 503 after 2", res.StatusCode, calls.Load())
		}
	})
}

func TestNoResponse(t *testing.T) {
	// connection refused: a closed server
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	res := newClient(t, nil).Fetch(context.Background(), url)
	if res.FetchError == "" {
		t.Error("connection refused must set FetchError")
	}
	if res.StatusCode != 0 {
		t.Errorf("status = %d, want 0", res.StatusCode)
	}
}

func TestHeadersAndUserAgent(t *testing.T) {
	var gotUA, gotLang, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotLang = r.Header.Get("Accept-Language")
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	t.Setenv("ACRAWLER_TEST_PW", "s3cret")
	c := newClient(t, func(c *config.Config) {
		c.HTTP.UserAgent = "mybot/9"
		c.HTTP.Headers = map[string]string{"Accept-Language": "de"}
		c.HTTP.Auth.Basic = []config.BasicAuth{
			{URLPrefix: srv.URL, Username: "alice", PasswordEnv: "ACRAWLER_TEST_PW"},
		}
	})
	c.Fetch(context.Background(), srv.URL+"/x")

	if gotUA != "mybot/9" {
		t.Errorf("user-agent = %q", gotUA)
	}
	if gotLang != "de" {
		t.Errorf("accept-language = %q", gotLang)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("authorization = %q, want basic credentials from env", gotAuth)
	}
}

func TestAuthOnlyAppliesToMatchingPrefix(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := newClient(t, func(c *config.Config) {
		c.HTTP.Auth.Basic = []config.BasicAuth{
			{URLPrefix: "https://other.example.com", Username: "alice", Password: "x"},
		}
	})
	c.Fetch(context.Background(), srv.URL+"/x")
	if gotAuth != "" {
		t.Errorf("authorization must not be sent to non-matching hosts, got %q", gotAuth)
	}
}

func TestBodyTruncation(t *testing.T) {
	big := strings.Repeat("a", 8*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, big)
	}))
	defer srv.Close()

	res := newClient(t, func(c *config.Config) { c.Limits.MaxPageSizeKB = 4 }).Fetch(context.Background(), srv.URL)
	if !res.Truncated {
		t.Error("must be flagged truncated")
	}
	if len(res.Body) != 4*1024 {
		t.Errorf("body length = %d, want %d", len(res.Body), 4*1024)
	}

	// exactly at the limit: not truncated
	res = newClient(t, func(c *config.Config) { c.Limits.MaxPageSizeKB = 8 }).Fetch(context.Background(), srv.URL)
	if res.Truncated {
		t.Error("body exactly at limit must not be flagged")
	}
}

func TestCookieModes(t *testing.T) {
	mux := http.NewServeMux()
	var lastCookie string
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "42", Path: "/"})
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		lastCookie = r.Header.Get("Cookie")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("persistent stores cookies across requests", func(t *testing.T) {
		lastCookie = ""
		c := newClient(t, func(c *config.Config) { c.Advanced.CookieStorage = "persistent" })
		c.Fetch(context.Background(), srv.URL+"/set")
		c.Fetch(context.Background(), srv.URL+"/check")
		if !strings.Contains(lastCookie, "sid=42") {
			t.Errorf("cookie not persisted, got %q", lastCookie)
		}
	})

	t.Run("session mode does not persist across requests", func(t *testing.T) {
		lastCookie = ""
		c := newClient(t, nil) // default: session
		c.Fetch(context.Background(), srv.URL+"/set")
		c.Fetch(context.Background(), srv.URL+"/check")
		if lastCookie != "" {
			t.Errorf("session mode must not persist cookies, got %q", lastCookie)
		}
	})

	t.Run("configured auth cookies are always sent", func(t *testing.T) {
		lastCookie = ""
		c := newClient(t, func(c *config.Config) {
			c.HTTP.Auth.Cookies = []config.AuthCookie{{Name: "auth", Value: "tok"}}
		})
		c.Fetch(context.Background(), srv.URL+"/check")
		if !strings.Contains(lastCookie, "auth=tok") {
			t.Errorf("auth cookie missing, got %q", lastCookie)
		}
	})
}

func TestHSTS(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Strict-Transport-Security", "max-age=600; includeSubDomains")
	}))
	defer srv.Close()

	httpURL := strings.Replace(srv.URL, "https://", "http://", 1)

	t.Run("synthetic 307 after HSTS seen", func(t *testing.T) {
		hits.Store(0)
		c := newClient(t, nil, WithInsecureTLS())
		c.Fetch(context.Background(), srv.URL+"/a")
		res := c.Fetch(context.Background(), httpURL+"/b")
		if res.StatusCode != 307 || res.Status != "HSTS Policy" {
			t.Fatalf("got %d %q, want 307 HSTS Policy", res.StatusCode, res.Status)
		}
		if res.RedirectType != "hsts" {
			t.Errorf("redirect type = %q", res.RedirectType)
		}
		if !strings.HasPrefix(res.RedirectURL, "https://") {
			t.Errorf("redirect url = %q, want https upgrade", res.RedirectURL)
		}
		if hits.Load() != 1 {
			t.Errorf("server hit %d times, synthetic response must not touch the network", hits.Load())
		}
	})

	t.Run("respect_hsts=false disables emulation", func(t *testing.T) {
		hits.Store(0)
		c := newClient(t, func(c *config.Config) { c.Advanced.RespectHSTS = false }, WithInsecureTLS())
		c.Fetch(context.Background(), srv.URL+"/a")
		res := c.Fetch(context.Background(), httpURL+"/b")
		if res.RedirectType == "hsts" {
			t.Error("hsts emulation must be off")
		}
	})

	t.Run("max-age=0 clears the host", func(t *testing.T) {
		store := newHSTSStore()
		store.record("ex.com", "max-age=600")
		if !store.match("ex.com") {
			t.Fatal("must match after record")
		}
		store.record("ex.com", "max-age=0")
		if store.match("ex.com") {
			t.Error("max-age=0 must clear")
		}
	})

	t.Run("includeSubDomains matches children", func(t *testing.T) {
		store := newHSTSStore()
		store.record("ex.com", "max-age=600; includeSubDomains")
		if !store.match("sub.ex.com") {
			t.Error("subdomain must match")
		}
		store.record("solo.com", "max-age=600")
		if store.match("sub.solo.com") {
			t.Error("subdomain must not match without includeSubDomains")
		}
	})
}

func TestForceHTTPVersion(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proto=%s", r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	t.Run("default negotiates HTTP/2", func(t *testing.T) {
		res := newClient(t, nil, WithInsecureTLS()).Fetch(context.Background(), srv.URL)
		if res.FetchError != "" {
			t.Fatalf("unexpected error: %s", res.FetchError)
		}
		if res.HTTPVersion != "HTTP/2.0" {
			t.Errorf("http version = %q, want HTTP/2.0", res.HTTPVersion)
		}
	})

	t.Run("version 1.1 forces HTTP/1.1", func(t *testing.T) {
		res := newClient(t, func(c *config.Config) { c.HTTP.Version = "1.1" }, WithInsecureTLS()).
			Fetch(context.Background(), srv.URL)
		if res.FetchError != "" {
			t.Fatalf("unexpected error: %s", res.FetchError)
		}
		if res.HTTPVersion != "HTTP/1.1" {
			t.Errorf("http version = %q, want HTTP/1.1", res.HTTPVersion)
		}
	})
}

func TestBrowserHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	t.Run("sent by default", func(t *testing.T) {
		newClient(t, nil).Fetch(context.Background(), srv.URL)
		// Matches Screaming Frog v24.1's measured default request profile.
		if a := got.Get("Accept"); !strings.Contains(a, "text/html") {
			t.Errorf("Accept = %q, want a browser navigational value", a)
		}
		if got.Get("Cache-Control") != "no-cache" || got.Get("Pragma") != "no-cache" {
			t.Errorf("Cache-Control/Pragma = %q/%q, want no-cache/no-cache like SF",
				got.Get("Cache-Control"), got.Get("Pragma"))
		}
		// SF sends no Accept-Language, so neither do we by default.
		if al := got.Get("Accept-Language"); al != "" {
			t.Errorf("Accept-Language = %q, want none by default (SF sends none)", al)
		}
		// We must not set Accept-Encoding: the transport adds its own "gzip" and
		// transparently decompresses only while we leave the header untouched.
		if ae := got.Get("Accept-Encoding"); ae != "gzip" {
			t.Errorf("Accept-Encoding = %q, want the transport's transparent \"gzip\"", ae)
		}
	})

	t.Run("disabled via browser_headers=false", func(t *testing.T) {
		newClient(t, func(c *config.Config) { c.HTTP.BrowserHeaders = false }).
			Fetch(context.Background(), srv.URL)
		if got.Get("Accept") != "" {
			t.Errorf("Accept = %q, want none when browser headers are off", got.Get("Accept"))
		}
	})

	t.Run("explicit header overrides the browser default", func(t *testing.T) {
		newClient(t, func(c *config.Config) {
			c.HTTP.Headers = map[string]string{"Accept": "application/json"}
		}).Fetch(context.Background(), srv.URL)
		if got.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want the configured value to win", got.Get("Accept"))
		}
	})
}

func TestInvalidURL(t *testing.T) {
	res := newClient(t, nil).Fetch(context.Background(), "http://\x00bad")
	if res.FetchError == "" {
		t.Error("invalid URL must set FetchError")
	}
}
