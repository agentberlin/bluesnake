package crawler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/render"
)

// blobRecorder is a Sink that also implements BlobSink, recording every blob.
type blobRecorder struct {
	mu    sync.Mutex
	blobs map[string][]byte // "url|kind" -> data
}

var (
	_ Sink     = (*blobRecorder)(nil)
	_ BlobSink = (*blobRecorder)(nil)
)

func (b *blobRecorder) Page(*PageRecord) error    { return nil }
func (b *blobRecorder) FrontierDone(string) error { return nil }

func (b *blobRecorder) Blob(url, kind string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blobs == nil {
		b.blobs = map[string][]byte{}
	}
	cp := append([]byte(nil), data...)
	b.blobs[url+"|"+kind] = cp
	return nil
}

func (b *blobRecorder) get(url, kind string) ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.blobs[url+"|"+kind]
	return d, ok
}

// With rendering on, extraction.store_rendered_html persists the post-JS DOM
// and rendering.screenshots persists a PNG — both via the BlobSink, the same
// path raw store_html uses.
func TestStoreRenderedHTMLAndScreenshot(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	cfg.Extraction.StoreRenderedHTML = true
	cfg.Rendering.Screenshots = true
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping rendered-HTML/screenshot persistence test")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsPage))
	}))
	defer srv.Close()

	sink := &blobRecorder{}
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}

	rendered, ok := sink.get(srv.URL+"/", "rendered_html")
	if !ok {
		t.Fatal("rendered_html blob not stored")
	}
	if !bytes.Contains(rendered, []byte("JS Title")) {
		t.Error("rendered_html blob does not contain the JS-applied title (got raw HTML?)")
	}
	shot, ok := sink.get(srv.URL+"/", "screenshot")
	if !ok {
		t.Fatal("screenshot blob not stored")
	}
	// chromedp.FullScreenshot encodes JPEG (FF D8 FF ...)
	if len(shot) < 3 || !bytes.Equal(shot[:3], []byte{0xff, 0xd8, 0xff}) {
		t.Errorf("screenshot blob is not a JPEG (len %d)", len(shot))
	}
}

// Without the flags, neither blob is written even when rendering is on.
func TestStoreRenderedHTMLDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsPage))
	}))
	defer srv.Close()

	sink := &blobRecorder{}
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.get(srv.URL+"/", "rendered_html"); ok {
		t.Error("rendered_html stored although store_rendered_html is false")
	}
	if _, ok := sink.get(srv.URL+"/", "screenshot"); ok {
		t.Error("screenshot stored although rendering.screenshots is false")
	}
}
