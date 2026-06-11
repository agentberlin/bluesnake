package crawler

import "testing"

// httptest servers negotiate HTTP/1.1, so the protocol recorded from
// fetch.Result.HTTPVersion must surface on the page record verbatim.
func TestHTTPVersionRecorded(t *testing.T) {
	s := newSite(t, map[string]string{
		"/": "<p>home</p>",
	})
	res := crawl(t, s, nil)

	rec := s.page(res, "/")
	if rec == nil || rec.State != StateCrawled {
		t.Fatalf("seed page = %+v", rec)
	}
	if rec.HTTPVersion != "HTTP/1.1" {
		t.Errorf("HTTPVersion = %q, want %q", rec.HTTPVersion, "HTTP/1.1")
	}
}
