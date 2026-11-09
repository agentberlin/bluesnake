package nearcopy

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestNearCopiesOnABundle runs the step over a real crawl's bundle and reports
// the numbers the spec checks it by — pages compared, pairs at 80% and 90%,
// the most and the median near copies a page has, recall against a far more
// permissive candidate rule, the spot-check pairs, and the step's time and
// peak heap. It runs only when BLUESNAKE_NEARCOPY_BUNDLE names a bundle
// (bluesnake.pages/3, gzipped or not):
//
//	BLUESNAKE_NEARCOPY_BUNDLE=crawl.jsonl.gz go test ./internal/nearcopy -run OnABundle -v -timeout 2h
//
// BLUESNAKE_NEARCOPY_MIN_WORDS (default 50) sets the minimum words; pages are
// indexable only, as Berlin crawls. The bundle carries no signatures, so they
// are computed from content_text, as for a crawl that ran with the check off.
// The time and memory are the step's over a bundle stream; over a stored crawl
// the SQLite read replaces the JSON one.
func TestNearCopiesOnABundle(t *testing.T) {
	path := os.Getenv("BLUESNAKE_NEARCOPY_BUNDLE")
	if path == "" {
		t.Skip("BLUESNAKE_NEARCOPY_BUNDLE names no bundle")
	}
	minWords := 50
	if v := os.Getenv("BLUESNAKE_NEARCOPY_MIN_WORDS"); v != "" {
		var err error
		if minWords, err = strconv.Atoi(v); err != nil {
			t.Fatal(err)
		}
	}
	src := bundleSource(t, path)
	settings := func(th int) Settings {
		return Settings{Threshold: th, MinWords: minWords, IndexableOnly: true}
	}

	var res80 *Result
	start := time.Now()
	peak := peakHeap(func() {
		var err error
		if res80, err = Run(src, settings(80), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("80%%: %s, peak heap %.0f MB (bundle decoding included)", time.Since(start).Round(time.Millisecond), float64(peak)/(1<<20))
	report(t, "80%", res80)
	res90, err := Run(src, settings(90), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report(t, "90%", res90)

	// Recall: every pair at 80% or more, by the step's rule and by the 60%
	// rule — two-row bands, an exact check from 24 agreeing values — which
	// loses a pair at 80% with probability ~1e-14: the all-pairs answer, at a
	// fraction of its cost.
	collect := func(b Bands) map[[2]string]int32 {
		pairs := map[[2]string]int32{}
		if _, err := runWith(src, settings(80), t.TempDir(), b, func(a, c string, bp int32) {
			pairs[[2]string{min(a, c), max(a, c)}] = bp
		}); err != nil {
			t.Fatal(err)
		}
		return pairs
	}
	got, ref := collect(Banding(80)), collect(Banding(60))
	missing := 0
	for p, bp := range ref {
		if _, ok := got[p]; !ok {
			missing++
			if missing <= 5 {
				t.Logf("missed: %s %s at %.4f", p[0], p[1], float64(bp)/10000)
			}
		}
	}
	t.Logf("recall at 80%%: %d of %d reference pairs missing (%.2g); %d found that the reference lacks",
		missing, len(ref), float64(missing)/float64(max(1, len(ref))), len(got)-(len(ref)-missing))
	if len(ref) > 0 && float64(missing)/float64(len(ref)) >= 1e-3 {
		t.Errorf("missing %d of %d pairs at 80%% or more — once in %.0f, want under once in 1,000",
			missing, len(ref), float64(len(ref))/float64(missing))
	}

	pages80 := map[string]PageResult{}
	for _, p := range res80.Pages {
		pages80[p.URL] = p
	}
	for _, pair := range [][2]string{
		{"https://www.miniorange.com/atlassian/rest-api-authentication-using-aws-cognito-as-oauth-provider/",
			"https://www.miniorange.com/atlassian/rest-api-authentication-using-google-apps-as-oauth-provider"},
		{"https://www.miniorange.com/iam/content-library/admin-docs/configure-external-identity-providers",
			"https://www.miniorange.com/iam/content-library/partner-portal-docs/configure-external-identity-providers"},
		{"https://faq.miniorange.com/kb-tag/saml-link/", "https://faq.miniorange.com/kb-tag/sso-link/"},
		{"https://www.miniorange.com/iam/content-library/admin-docs/index", ""},
	} {
		p, compared := pages80[pair[0]]
		switch {
		case !compared:
			t.Logf("spot check %s: not compared (near_copies null)", pair[0])
		case pair[1] == "":
			t.Logf("spot check %s: compared, %d near copies", pair[0], p.Count)
		default:
			share := "not listed"
			for _, c := range p.Copies {
				if c.URL == pair[1] {
					share = strconv.FormatFloat(c.Share, 'f', 4, 64)
				}
			}
			t.Logf("spot check %s ~ %s: %s", pair[0], pair[1], share)
		}
	}
}

// report logs the shape of one result.
func report(t *testing.T, label string, res *Result) {
	t.Helper()
	var counts []int
	most := 0
	for _, p := range res.Pages {
		if p.Count > 0 {
			counts = append(counts, p.Count)
		}
		most = max(most, p.Count)
	}
	slices.Sort(counts)
	median := 0
	if len(counts) > 0 {
		median = counts[len(counts)/2]
	}
	t.Logf("%s: %d pages compared, %d pairs, %d pages with any, most a page has %d, median among those with any %d",
		label, len(res.Pages), res.Pairs, len(counts), most, median)
}

// bundleSource streams a bundle's page lines as pages.
func bundleSource(t *testing.T, path string) Source {
	return func(fn func(*Page) error) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		var r io.Reader = bufio.NewReaderSize(f, 1<<20)
		if strings.HasSuffix(path, ".gz") {
			gz, err := gzip.NewReader(r)
			if err != nil {
				return err
			}
			defer gz.Close()
			r = gz
		}
		dec := json.NewDecoder(r)
		for line := 0; ; line++ {
			var p struct {
				Format      string `json:"format"`
				Record      string `json:"record"`
				URL         string `json:"url"`
				Scope       string `json:"scope"`
				State       string `json:"state"`
				Indexable   bool   `json:"indexable"`
				WordCount   int    `json:"word_count"`
				RelPrev     string `json:"rel_prev"`
				ContentHash string `json:"content_hash"`
				ContentText string `json:"content_text"`
			}
			if err := dec.Decode(&p); err == io.EOF {
				return nil
			} else if err != nil {
				return err
			}
			if line == 0 && !strings.HasPrefix(p.Format, "bluesnake.pages/") {
				t.Fatalf("%s is not a bluesnake bundle (format %q)", path, p.Format)
			}
			if p.Format != "" || p.Record != "" {
				continue // the header and crawl-level records
			}
			if err := fn(&Page{URL: p.URL, Internal: p.Scope == "internal", Crawled: p.State == "crawled",
				Indexable: p.Indexable, Paginated: p.RelPrev != "", WordCount: p.WordCount,
				Hash: p.ContentHash, Text: p.ContentText}); err != nil {
				return err
			}
		}
	}
}
