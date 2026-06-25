package render

import (
	"context"
	"strings"
	"testing"
)

// R9b: chromedp.OuterHTML does not serialize shadow roots, so links inside open
// OR closed shadow DOM were invisible to the parser. Screaming Frog pierces both
// (Link Origin: Rendered HTML). rendering.flatten_shadow_dom (default true)
// inlines shadow content into the rendered snapshot so it flows through the same
// parse + structured pipeline.
//
// Hrefs are built by concatenation ('/r'+'-shadow') so the literal "/r-shadow"
// appears in res.HTML ONLY once the shadow content is inlined into the
// serialized DOM — never from the inline <script> source. A nested host (a
// shadow root that itself contains another shadow host) pins the recursive walk.
const shadowPage = `<html><head><title>shadow</title></head><body>
<div id="host-open"></div>
<div id="host-closed"></div>
<div id="host-nested"></div>
<script>
  var o=document.getElementById('host-open').attachShadow({mode:'open'});
  o.innerHTML='<a href="/r'+'-shadow">x</a>';
  var c=document.getElementById('host-closed').attachShadow({mode:'closed'});
  c.innerHTML='<a href="/r'+'-shadowclosed">x</a>';
  var n=document.getElementById('host-nested').attachShadow({mode:'open'});
  var inner=document.createElement('div'); n.appendChild(inner);
  var ni=inner.attachShadow({mode:'open'});
  ni.innerHTML='<a href="/r'+'-shadownested">x</a>';
</script></body></html>`

func TestRenderFlattensShadowDOMLinks(t *testing.T) {
	cfg := requireChrome(t)
	// default flatten_shadow_dom is true; assert that explicitly for clarity
	if !cfg.Rendering.FlattenShadowDOM {
		t.Fatal("flatten_shadow_dom should default true")
	}
	srv := htmlOnly(shadowPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/r-shadow", "/r-shadowclosed", "/r-shadownested"} {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("rendered snapshot missing shadow-DOM link %q (R9b)", want)
		}
	}
}

// The knob gates the behaviour: with flatten_shadow_dom off, the snapshot is the
// plain OuterHTML and shadow content stays invisible (old behaviour).
func TestRenderShadowDOMNotFlattenedWhenDisabled(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.FlattenShadowDOM = false
	srv := htmlOnly(shadowPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, notWant := range []string{"/r-shadow", "/r-shadowclosed", "/r-shadownested"} {
		if strings.Contains(res.HTML, notWant) {
			t.Errorf("flatten_shadow_dom=false but snapshot contains shadow link %q", notWant)
		}
	}
}

// A <slot> renders its assigned light content when filled, and its fallback
// children only when empty. Flattening must serialize what is actually rendered:
// the filled slot's fallback must NOT leak into the snapshot (it would over-count
// links/words vs SF's composed tree), while an empty slot's fallback must.
const slotPage = `<html><head><title>slot</title></head><body>
<div id="filled"><a href="/r-slotted">x</a></div>
<div id="empty"></div>
<script>
  var f=document.getElementById('filled').attachShadow({mode:'open'});
  f.innerHTML='<slot><a href="/r'+'-fallbackfilled">x</a></slot>';
  var e=document.getElementById('empty').attachShadow({mode:'open'});
  e.innerHTML='<slot><a href="/r'+'-fallbackempty">x</a></slot>';
</script></body></html>`

func TestRenderFlattenSlotFallbackNotOverCounted(t *testing.T) {
	cfg := requireChrome(t)
	srv := htmlOnly(slotPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Assigned (rendered) light content is present.
	if !strings.Contains(res.HTML, "/r-slotted") {
		t.Error("snapshot missing the slotted (assigned) link")
	}
	// Filled slot's fallback is NOT rendered — must not be serialized.
	if strings.Contains(res.HTML, "/r-fallbackfilled") {
		t.Error("snapshot wrongly contains a FILLED slot's fallback content (over-count vs SF composed tree)")
	}
	// Empty slot's fallback IS rendered — must be serialized.
	if !strings.Contains(res.HTML, "/r-fallbackempty") {
		t.Error("snapshot missing an EMPTY slot's fallback content (it is the rendered content)")
	}
}
