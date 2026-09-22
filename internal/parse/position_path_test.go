package parse

// Link.PositionPath retention: the path the link-position rules matched, kept
// alongside the rule name they produced. ElemPath stays pure-positional for
// Screaming Frog parity (sfElemPath), so it can never carry the class and id
// names a downstream classifier needs — terms like "masthead", "breadcrumb" or
// "sticky-header" appear ONLY in a class or id. Without PositionPath a site
// built from <div class="site-footer"> rather than <footer> leaves no artefact
// saying why its links are furniture.

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// The class-annotated path is stored; the pure-positional elem path is not
// touched by it. Both come off the same DOM node, one DOM walk each.
func TestPositionPathCarriesClassWhileElemPathStaysPositional(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><div class="site-footer"><a href="/imprint">Imprint</a></div></body></html>`, nil, nil)
	l := findLink(f, Hyperlink, "https://ex.com/imprint")
	if l == nil {
		t.Fatal("link missing")
	}
	if !strings.Contains(l.PositionPath, "site-footer") {
		t.Errorf("position path = %q, want it to carry the site-footer class", l.PositionPath)
	}
	if l.Position != "footer" {
		t.Errorf("position = %q, want footer", l.Position)
	}
	// The stored elem path must stay the SF-parity pure-positional form: its
	// 37%-vs-17% agreement argument is the reason it does not carry qualifiers.
	if strings.Contains(l.ElemPath, "site-footer") || strings.Contains(l.ElemPath, "@class") {
		t.Errorf("elem path = %q, want no id/class qualifier", l.ElemPath)
	}
	if l.ElemPath != "//body/div/a" {
		t.Errorf("elem path = %q, want //body/div/a", l.ElemPath)
	}
}

// PositionPath is EXACTLY what positionPath() returns — not a new variant. The
// sharpest case is the multi-token class SF drops: the path must drop it too,
// which is why the link classifies as content rather than footer.
func TestPositionPathIsExactlyTheMatchedPath(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><div class="col footer-col"><a href="/x">x</a></div></body></html>`, nil, nil)
	l := findLink(f, Hyperlink, "https://ex.com/x")
	if l == nil {
		t.Fatal("link missing")
	}
	if strings.Contains(l.PositionPath, "footer-col") || strings.Contains(l.PositionPath, "@class") {
		t.Errorf("position path = %q, want the multi-token class dropped (SF behaviour)", l.PositionPath)
	}
	if l.PositionPath != "/html/body/div/a" {
		t.Errorf("position path = %q, want /html/body/div/a", l.PositionPath)
	}
	if l.Position != "content" {
		t.Errorf("position = %q, want content — the dropped class must not match", l.Position)
	}
}

// An id qualifier rides the path the same way a single-token class does.
func TestPositionPathCarriesID(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><div id="masthead"><a href="/home">Home</a></div></body></html>`, nil, nil)
	l := findLink(f, Hyperlink, "https://ex.com/home")
	if l == nil {
		t.Fatal("link missing")
	}
	if l.PositionPath != "/html/body/div[@id='masthead']/a" {
		t.Errorf("position path = %q, want the id qualifier", l.PositionPath)
	}
	// "masthead" is not one of bluesnake's position terms — the label is empty,
	// and the path is the only thing that can tell a consumer otherwise. That is
	// the whole point of storing it.
	if l.Position != "content" {
		t.Errorf("position = %q, want content", l.Position)
	}
}

// A link no rule labels still keeps its path: the verdict is empty only because
// no configured term matched, which says nothing about the path's usefulness.
func TestPositionPathKeptWhenNoRuleMatches(t *testing.T) {
	cfg := func(c *config.Config) {
		c.LinkPositions = []config.LinkPosition{{Name: "footer", Match: "footer"}}
	}
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><main><a href="/x">x</a></main></body></html>`, nil, cfg)
	l := findLink(f, Hyperlink, "https://ex.com/x")
	if l == nil {
		t.Fatal("link missing")
	}
	if l.Position != "" {
		t.Errorf("position = %q, want empty (no rule matches)", l.Position)
	}
	if l.PositionPath != "/html/body/main/a" {
		t.Errorf("position path = %q, want it kept even with no matching rule", l.PositionPath)
	}
}

// Gated by StoreLinkPaths exactly as Position is: with link-path storage off,
// neither is written.
func TestPositionPathEmptyWhenLinkPathsDisabled(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<footer><a href="/x">x</a></footer>
	</body></html>`, nil, func(c *config.Config) { c.StoreLinkPaths = false })
	l := findLink(f, Hyperlink, "https://ex.com/x")
	if l == nil {
		t.Fatal("link missing")
	}
	if l.Position != "" || l.PositionPath != "" {
		t.Errorf("position = %q / position path = %q, want both empty when StoreLinkPaths is off",
			l.Position, l.PositionPath)
	}
}

// Uncrawlable edges append outside addLink; they must carry the path too, or
// the evidence is inconsistent across link types.
func TestPositionPathOnUncrawlableLinks(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<div class="site-footer"><a href="javascript:openMenu()">menu</a></div>
		<div class="navbar"><span href="/span-href">not a link</span></div>
	</body></html>`, nil, func(c *config.Config) { c.Links.Uncrawlable.Store = true })
	var seen int
	for _, l := range f.Links {
		if l.Type != Uncrawlable {
			continue
		}
		seen++
		if l.PositionPath == "" {
			t.Errorf("uncrawlable link %q has no position path", l.Raw)
		}
	}
	if seen != 2 {
		t.Fatalf("uncrawlable links = %d, want 2", seen)
	}
}
