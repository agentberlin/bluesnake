package runner

// P11 / X-03,X-05 (MEMORY-SCALING.md §13): the identical-content short-circuit
// (skip_identical_content_links) is backed by the on-disk content_hash table, so
// the canonical owner of a raw-body hash must survive a resume — a byte-identical
// twin crawled in a LATER session must be marked DuplicateOf the SAME canonical a
// straight crawl picks, and the admitted set must match. The earlier resume tests
// never crossed identical content with an interrupt boundary, so this path was
// unpinned.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// identicalShellServer serves a hub linking to N byte-identical leaf shells, so
// the first leaf crawled becomes the canonical and the rest are its duplicates.
func identicalShellServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	const shell = `<html><head><title>Shell</title></head><body><p>one identical shell body shared by every dup url</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			var b strings.Builder
			b.WriteString("<html><head><title>Hub</title></head><body>")
			for i := 1; i <= n; i++ {
				fmt.Fprintf(&b, `<a href="/dup%d">d%d</a>`, i, i)
			}
			b.WriteString("</body></html>")
			fmt.Fprint(w, b.String())
			return
		}
		fmt.Fprint(w, shell) // every /dupN is byte-identical
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dupMap returns url -> duplicate_of for every crawled page (relative paths).
func dupMap(t *testing.T, dir, id, base string) map[string]string {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for url, rec := range pages {
		out[strings.TrimPrefix(url, base)] = strings.TrimPrefix(rec.DuplicateOf, base)
	}
	return out
}

func TestResume_ContentHashCanonicalSurvivesResume(t *testing.T) {
	const dups = 4 // /dup1..dup4
	srv := identicalShellServer(t, dups)
	base := srv.URL

	// Baseline: a straight crawl. /dup1 (crawled first) is canonical; the rest are
	// DuplicateOf /dup1.
	straightDir := t.TempDir()
	if _, err := New(straightDir, nil).Run(context.Background(),
		queue.JobSpec{URL: base + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	sInfos, _ := store.ListCrawls(straightDir)
	straight := dupMap(t, straightDir, sInfos[0].ID, base)
	// Sanity: the short-circuit actually fired (some page is a duplicate).
	anyDup := false
	for _, d := range straight {
		if d != "" {
			anyDup = true
		}
	}
	if !anyDup {
		t.Fatal("straight crawl produced no duplicates — fixture/short-circuit is vacuous")
	}

	// Interrupt after 2 pages (/, /dup1): the canonical is claimed in session 1,
	// its twins are still pending.
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: base + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	crawlID := obs.startID

	// Resume to completion.
	status, err := New(dir, &recObs{}).Run(context.Background(),
		queue.JobSpec{ResumeID: crawlID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resumed crawl status = %q, want completed", status)
	}

	// The resumed crawl's duplicate map must match the straight crawl exactly: the
	// content_hash canonical claimed in session 1 governs the twins crawled in
	// session 2.
	resumed := dupMap(t, dir, crawlID, base)
	if len(resumed) != len(straight) {
		t.Fatalf("resumed crawled %d pages, straight crawled %d", len(resumed), len(straight))
	}
	keys := make([]string, 0, len(straight))
	for k := range straight {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if resumed[k] != straight[k] {
			t.Errorf("page %s: resumed duplicate_of = %q, straight = %q — content_hash canonical did not survive resume",
				k, resumed[k], straight[k])
		}
	}
}
