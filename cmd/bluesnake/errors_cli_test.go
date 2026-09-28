package main

// A failing command prints its error once. cobra prints every error a RunE
// returns as "Error: <err>", so a command that also printed it itself showed
// it twice, and an interrupted crawl added "Error: interrupted" under its
// resume hint.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestErrorsPrintOnce(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)
	tmp := t.TempDir()
	urls := filepath.Join(tmp, "urls.txt")
	if err := os.WriteFile(urls, []byte(srv.URL+"/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	badCfg := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badCfg, []byte("speed:\n  nope: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const badSet = `override "nope=1"`

	cases := []struct {
		name string
		args []string
		code int
		msg  string // the start of the error, which follows "Error: "
	}{
		{"crawl --set", []string{"crawl", srv.URL + "/", "--store-dir", dir, "--set", "nope=1"}, 2, badSet},
		{"list --set", []string{"list", urls, "--store-dir", dir, "--set", "nope=1"}, 2, badSet},
		{"resume --force --set", []string{"resume", id, "--store-dir", dir, "--force", "--set", "nope=1"}, 2, badSet},
		{"resume --set without --force", []string{"resume", id, "--store-dir", dir, "--set", "nope=1"}, 2, "resume uses the config stored with the crawl"},
		{"resume unknown crawl", []string{"resume", "no-such-crawl", "--store-dir", dir}, 2, `crawl "no-such-crawl" not found`},
		{"crawl bad seed", []string{"crawl", "not a url", "--store-dir", dir, "--setup", "defaults"}, 1, "url must be a full URL"},
		{"config show --set", []string{"config", "show", "--store-dir", dir, "--set", "nope=1"}, 2, badSet},
		{"config validate", []string{"config", "validate", badCfg}, 2, badCfg + ": config:"},
		{"tools robots", []string{"tools", "robots"}, 2, "provide --site, --robots-file, or at least one URL"},
		// errors cobra raises itself were already printed once, and still are
		{"unknown flag", []string{"crawl", srv.URL + "/", "--nope"}, 1, "unknown flag: --nope"},
		{"invalid flag value", []string{"crawl", srv.URL + "/", "--threads", "abc"}, 1, `invalid argument "abc" for "--threads" flag`},
		{"unknown command", []string{"robots"}, 1, `unknown command "robots" for "bluesnake"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, code := runSplitT(t, c.args...)
			if code != c.code {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, c.code, stderr)
			}
			if !strings.HasPrefix(stderr, "Error: "+c.msg) || strings.Count(stderr, c.msg) != 1 {
				t.Errorf("stderr does not print %q once, after \"Error: \":\n%s", c.msg, stderr)
			}
			if strings.Contains(stdout, c.msg) {
				t.Errorf("the error reached stdout:\n%s", stdout)
			}
		})
	}
}

// TestUnknownCommandKeepsUsageHint: cobra's pointer to --help stays under an
// unknown command's error.
func TestUnknownCommandKeepsUsageHint(t *testing.T) {
	_, stderr, _ := runSplitT(t, "robots")
	want := "Error: unknown command \"robots\" for \"bluesnake\"\nRun 'bluesnake --help' for usage.\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// hangServer serves "/" linking to "/next" and holds "/next" open until the
// request is cancelled. hit closes when "/next" is first requested, which is
// when a crawl of this site is under way.
func hangServer(t *testing.T) (srv *httptest.Server, hit <-chan struct{}) {
	t.Helper()
	requested := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><body><a href="/next">next</a></body></html>`)
		case "/next":
			once.Do(func() { close(requested) })
			select {
			case <-r.Context().Done():
			case <-release:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	return srv, requested
}

// TestInterruptPrintsOnlyTheResumeHint: an interrupted crawl, list or resume
// exits 3 and reports the interrupt with its resume hint alone.
func TestInterruptPrintsOnlyTheResumeHint(t *testing.T) {
	cases := []struct {
		name string
		// args builds the command for a store dir and a hangServer, and
		// returns the crawl id the hint names ("" = the "Crawl ID:" on stdout)
		// and what stderr prints before the hint.
		args func(t *testing.T, dir string, srv *httptest.Server) (args []string, id, before string)
	}{
		{"crawl", func(t *testing.T, dir string, srv *httptest.Server) ([]string, string, string) {
			return []string{"crawl", srv.URL + "/", "--store-dir", dir, "--setup", "defaults"}, "", ""
		}},
		{"list", func(t *testing.T, dir string, srv *httptest.Server) ([]string, string, string) {
			urls := filepath.Join(t.TempDir(), "urls.txt")
			if err := os.WriteFile(urls, []byte(srv.URL+"/next\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{"list", urls, "--store-dir", dir}, "", "list mode: 1 URLs\n"
		}},
		{"resume", func(t *testing.T, dir string, srv *httptest.Server) ([]string, string, string) {
			id := forgeInterrupted(t, dir, srv) // its frontier holds /next
			return []string{"resume", id, "--store-dir", dir}, id, ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, hit := hangServer(t)
			dir := t.TempDir()
			args, id, before := c.args(t, dir, srv)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stderr syncBuffer
			type result struct {
				out  string
				code int
			}
			done := make(chan result, 1)
			go func() {
				out, code := runSplit(ctx, &stderr, args...)
				done <- result{out, code}
			}()
			select {
			case <-hit:
			case res := <-done:
				t.Fatalf("exited %d before reaching /next\nstdout:\n%s\nstderr:\n%s", res.code, res.out, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("/next not requested within 10s")
			}
			cancel()

			var res result
			select {
			case res = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("interrupted command did not exit")
			}
			if res.code != 3 {
				t.Errorf("exit %d, want 3", res.code)
			}
			if id == "" {
				m := regexp.MustCompile(`(?m)^Crawl ID: (\S+)$`).FindStringSubmatch(res.out)
				if m == nil {
					t.Fatalf("no Crawl ID on stdout:\n%s", res.out)
				}
				id = m[1]
			}
			want := before + fmt.Sprintf("crawl interrupted — resume with: bluesnake resume %s --store-dir %s\n", id, dir)
			if got := stderr.String(); got != want {
				t.Errorf("stderr = %q\nwant only the resume hint: %q", got, want)
			}
		})
	}
}
