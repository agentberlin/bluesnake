package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func TestFetchWithOverride(t *testing.T) {
	var gotUA, gotExtra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotExtra = r.Header.Get("X-Probe")
	}))
	defer srv.Close()

	cfg := config.Default()
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	c.FetchWith(context.Background(), srv.URL, Override{
		UserAgent: "GPTBot/1.2 (+https://openai.com/gptbot)",
		Headers:   map[string]string{"X-Probe": "1"},
	})
	if gotUA != "GPTBot/1.2 (+https://openai.com/gptbot)" || gotExtra != "1" {
		t.Errorf("override not applied: UA=%q X-Probe=%q", gotUA, gotExtra)
	}

	// The zero override keeps the configured profile.
	c.Fetch(context.Background(), srv.URL)
	if gotUA != cfg.HTTP.UserAgent {
		t.Errorf("plain Fetch UA = %q, want the configured one", gotUA)
	}
}
