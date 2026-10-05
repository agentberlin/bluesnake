package parse

import (
	"slices"
	"strings"
	"testing"
)

func authorsOf(t *testing.T, body string) []Author {
	t.Helper()
	return parseHTML(t, "https://ex.com/post", body, nil, nil).Authors
}

func checkAuthors(t *testing.T, got, want []Author) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("authors =\n  %+v\nwant\n  %+v", got, want)
	}
}

// Each of the five sources is read where it lands, and the evidence comes out
// in document order, each entry naming where it was found.
func TestAuthorsFromEverySourceInDocumentOrder(t *testing.T) {
	got := authorsOf(t, `<html><head>
		<meta name="author" content="  Jane   Doe ">
		<meta property="article:author" content="https://ex.com/people/jane">
		<meta property="article:author" content="Jane Doe">
		<link rel="author" href="/humans.txt">
	</head><body><article>
		<a rel="author" href="/people/jane">Jane Doe</a>
		<div itemprop="author" itemscope itemtype="https://schema.org/Person">
			<span itemprop="name">Jane Doe</span>
			<a itemprop="url" href="/people/jane">profile</a>
		</div>
		<p class="post-byline">By Jane Doe</p>
	</article></body></html>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorMeta, Name: "Jane Doe"},
		{Source: AuthorArticle, URL: "https://ex.com/people/jane"},
		{Source: AuthorArticle, Name: "Jane Doe"},
		{Source: AuthorRel, URL: "https://ex.com/humans.txt"},
		{Source: AuthorRel, Name: "Jane Doe", URL: "https://ex.com/people/jane"},
		{Source: AuthorMicrodata, Name: "Jane Doe", URL: "https://ex.com/people/jane"},
		{Source: AuthorByline, Name: "By Jane Doe"},
	})
}

// A page that says nothing about its author has no evidence, not an empty entry.
func TestAuthorsAbsentWhenThePageNamesNone(t *testing.T) {
	checkAuthors(t, authorsOf(t, `<html><head>
		<meta name="author" content="  ">
		<meta property="article:author" content="">
	</head><body><p>No byline here.</p><img class="author-avatar" src="/a.png"></body></html>`), nil)
}

// The same evidence from the same source is one entry, the first; the same
// name from two sources is two, because which source said it is the contract.
func TestAuthorsAreDeduplicatedPerSource(t *testing.T) {
	got := authorsOf(t, `<html><head>
		<meta name="author" content="Jane Doe">
		<meta name="author" content="Jane Doe">
		<meta name="author" content="John Roe">
	</head><body>
		<span class="author">Jane Doe</span>
		<span class="author">Jane Doe</span>
	</body></html>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorMeta, Name: "Jane Doe"},
		{Source: AuthorMeta, Name: "John Roe"},
		{Source: AuthorByline, Name: "Jane Doe"},
	})
}

// rel=author is a token: it reads alongside other rel values, and only an
// http(s) target is an author URL — a mailto keeps the name and drops the URL.
func TestRelAuthorIsATokenAndKeepsOnlyWebURLs(t *testing.T) {
	got := authorsOf(t, `<html><body>
		<a rel="author external" href="https://jane.example/">Jane</a>
		<a rel="author" href="mailto:john@ex.com">John</a>
		<a rel="authorship" href="/not-an-author">Nope</a>
	</body></html>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorRel, Name: "Jane", URL: "https://jane.example/"},
		{Source: AuthorRel, Name: "John"},
	})
}

// The rel=author anchor is still an ordinary hyperlink edge.
func TestRelAuthorAnchorStaysAHyperlink(t *testing.T) {
	f := parseHTML(t, "https://ex.com/post", `<a rel="author" href="/people/jane">Jane</a>`, nil, nil)
	if l := findLink(f, Hyperlink, "https://ex.com/people/jane"); l == nil || l.Anchor != "Jane" {
		t.Errorf("hyperlink = %+v, want the author anchor kept as a link", l)
	}
}

// A microdata author takes its name from a nested itemprop="name" — never one
// belonging to a nested item — or else its own value, and its URL from its own
// href or a nested itemprop="url".
func TestMicrodataAuthorReadsItsOwnItem(t *testing.T) {
	got := authorsOf(t, `<html><head>
		<meta itemprop="author" content="Head Meta">
	</head><body>
		<div itemprop="author" itemscope itemtype="https://schema.org/Person">
			<div itemprop="worksFor" itemscope itemtype="https://schema.org/Organization">
				<span itemprop="name">ACME</span>
				<a itemprop="url" href="https://acme.example/">ACME site</a>
			</div>
			<span itemprop="name">Jane Doe</span>
			<link itemprop="url" href="/people/jane">
		</div>
		<a itemprop="author" href="/people/john">John Roe</a>
		<span itemprop="creator author">Ann Poe</span>
		<link itemprop="author" href="/people/ann">
		<span itemprop="authority">Not an author</span>
		<div itemprop="author" itemscope><span itemprop="name">Bo Lee</span><span itemprop="url"> </span></div>
	</body></html>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorMicrodata, Name: "Head Meta"},
		{Source: AuthorMicrodata, Name: "Jane Doe", URL: "https://ex.com/people/jane"},
		{Source: AuthorMicrodata, Name: "John Roe", URL: "https://ex.com/people/john"},
		{Source: AuthorMicrodata, Name: "Ann Poe"},
		{Source: AuthorMicrodata, URL: "https://ex.com/people/ann"},
		// An empty url property is no URL — not the page's own, which an empty
		// href resolves to.
		{Source: AuthorMicrodata, Name: "Bo Lee"},
	})
}

// A byline is an element whose class or id holds the WORD author or byline —
// split at punctuation and camelCase — not any value containing the letters.
func TestBylineMatchesTheWordInAClassOrID(t *testing.T) {
	for _, tt := range []struct {
		attr string
		want bool
	}{
		{`class="author"`, true},
		{`class="entry-meta author-name"`, true},
		{`class="c-byline__text"`, true},
		{`class="postAuthor"`, true},
		{`class="AuthorBox"`, true},
		{`id="byline"`, true},
		{`class="post-authors"`, true},
		{`class="authority"`, false},
		{`class="coauthored"`, false},
		{`class="authorize-button"`, false},
		{`id="bylines2"`, false},
	} {
		got := authorsOf(t, `<html><body><div `+tt.attr+`>Jane Doe</div></body></html>`)
		if (len(got) == 1) != tt.want {
			t.Errorf("%s: authors = %+v, want byline %v", tt.attr, got, tt.want)
		}
	}
}

// Site furniture and hidden elements are not bylines: a nav's "Authors" link,
// the site footer, anything hidden by attribute or inline style. An article's
// own footer is where HTML puts its author, so it counts; the page's <body>
// carrying an author class (a CMS author archive) is not a byline of the page.
func TestBylineSkipsFurnitureAndHiddenElements(t *testing.T) {
	got := authorsOf(t, `<html><body class="archive author author-jane">
		<nav><a class="nav-authors" href="/authors">Authors</a></nav>
		<article>
			<h1>Post</h1>
			<footer class="entry-footer"><span class="byline">By Jane Doe</span></footer>
		</article>
		<div class="author" hidden>Hidden Attr</div>
		<div aria-hidden="true"><span class="author">Aria Hidden</span></div>
		<div style="display: none"><span class="author">Display None</span></div>
		<span class="author" style="VISIBILITY:hidden">Visibility Hidden</span>
		<footer><p class="author-credit">Site by Agency</p></footer>
	</body></html>`)
	checkAuthors(t, got, []Author{{Source: AuthorByline, Name: "By Jane Doe"}})
}

// Nested byline elements each count, like nested data-nosnippet: the outer
// line and the name inside it are both what the page shows.
func TestNestedBylinesEachCount(t *testing.T) {
	got := authorsOf(t, `<div class="byline">By <span class="author-name">Jane Doe</span> · 3 min read</div>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorByline, Name: "By Jane Doe · 3 min read"},
		{Source: AuthorByline, Name: "Jane Doe"},
	})
}

// A byline's text is collapsed and capped at 200 characters, so an author box
// wrapping a whole bio still yields the line that names the author.
func TestBylineTextIsCollapsedAndCapped(t *testing.T) {
	bio := "About the author: Jane Doe " + strings.Repeat("writes about crawlers ", 20)
	got := authorsOf(t, `<aside class="author-box"><p>`+bio+`</p></aside>`)
	if len(got) != 1 {
		t.Fatalf("authors = %+v, want one byline", got)
	}
	name := got[0].Name
	if n := len([]rune(name)); n > 200 {
		t.Errorf("byline is %d characters, want at most 200: %q", n, name)
	}
	if !strings.HasPrefix(name, "About the author: Jane Doe writes") || strings.HasSuffix(name, " ") {
		t.Errorf("byline = %q, want the collapsed start of the bio, trimmed", name)
	}
}

// A byline carries a URL when it links exactly one place: its own href, or its
// only link. Two links ("By Jane on March 3") cannot say which is the author.
func TestBylineURLComesFromItsOnlyLink(t *testing.T) {
	got := authorsOf(t, `<html><body>
		<span class="author vcard"><a class="url fn n" href="/author/jane/">Jane Doe</a></span>
		<a class="author-link" href="/author/john/">John Roe</a>
		<div class="byline">By <a href="/author/ann/">Ann Poe</a> on <a href="/2026/03/">March 3</a></div>
	</body></html>`)
	checkAuthors(t, got, []Author{
		{Source: AuthorByline, Name: "Jane Doe", URL: "https://ex.com/author/jane/"},
		{Source: AuthorByline, Name: "John Roe", URL: "https://ex.com/author/john/"},
		{Source: AuthorByline, Name: "By Ann Poe on March 3"},
	})
}

// A <template>'s contents are inert, so a byline inside one is not on the page.
func TestBylineInsideATemplateIsNotRead(t *testing.T) {
	checkAuthors(t, authorsOf(t, `<template><span class="author">Jane Doe</span></template>`), nil)
}
