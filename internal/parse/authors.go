package parse

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Author is one piece of evidence of who wrote a page: where it was found
// (Source) and what it said. Name or URL is "" when the source carried none —
// a <link rel="author"> has only a URL, a <meta name="author"> only a name.
type Author struct {
	Source string
	Name   string
	URL    string
}

// The sources an Author is read from.
const (
	AuthorMeta      = "meta"      // <meta name="author" content>
	AuthorArticle   = "article"   // <meta property="article:author" content>
	AuthorRel       = "rel"       // <a>/<link rel="author" href>
	AuthorMicrodata = "microdata" // itemprop="author"
	AuthorByline    = "byline"    // a visible element classed or id'd author/byline
)

// maxAuthorText caps a name read from an element's text, so an author box
// wrapping a whole bio yields the line that names the author, not the bio.
const maxAuthorText = 200

// addAuthor records one piece of evidence, keeping the first of identical
// ones. Evidence with neither a name nor a URL says nothing and is dropped.
func (p *parser) addAuthor(a Author) {
	if a.Name == "" && a.URL == "" {
		return
	}
	for _, have := range p.facts.Authors {
		if have == a {
			return
		}
	}
	p.facts.Authors = append(p.facts.Authors, a)
}

// authorURL resolves an author link, keeping it only when it is a web
// address: an author URL is a page about the author, which a mailto: or
// javascript: target is not.
func (p *parser) authorURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	if u := p.resolve(raw); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return ""
}

// metaAuthor reads the two meta tags that name an author: name="author", a
// name, and Open Graph's property="article:author", which is a profile URL by
// spec but in practice often a name, so a web URL goes in URL and anything
// else in Name.
func (p *parser) metaAuthor(n *html.Node) {
	content := collapseSpace(attr(n, "content"))
	if strings.EqualFold(attr(n, "name"), "author") {
		p.addAuthor(Author{Source: AuthorMeta, Name: content})
	}
	if strings.EqualFold(attr(n, "property"), "article:author") {
		if lower := strings.ToLower(content); strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			p.addAuthor(Author{Source: AuthorArticle, URL: p.authorURL(content)})
		} else {
			p.addAuthor(Author{Source: AuthorArticle, Name: content})
		}
	}
}

// relAuthor records an <a>, <area> or <link> whose rel holds the author
// token: the href as the URL and, for an anchor, its text as the name (the
// same text its hyperlink edge carries).
func (p *parser) relAuthor(n *html.Node, href string) {
	for _, rel := range strings.Fields(strings.ToLower(attr(n, "rel"))) {
		if rel != "author" {
			continue
		}
		a := Author{Source: AuthorRel, URL: p.authorURL(href)}
		if n.Data != "link" {
			a.Name = collapseSpace(subtreeText(n))
		}
		p.addAuthor(a)
		return
	}
}

// microdataAuthor records an element whose itemprop holds author. Its name is
// a nested itemprop="name" of its own item, else its own value; its URL is its
// own href, else a nested itemprop="url". The structured block keeps microdata
// types but not their properties, so this is the only place a microdata
// author survives.
func (p *parser) microdataAuthor(n *html.Node) {
	if !hasItemprop(n, "author") {
		return
	}
	a := Author{Source: AuthorMicrodata}
	if name := itemProperty(n, "name"); name != nil {
		a.Name = microdataText(name)
	} else {
		a.Name = microdataText(n)
	}
	if href := attr(n, "href"); href != "" {
		a.URL = p.authorURL(href)
	} else if u := itemProperty(n, "url"); u != nil {
		a.URL = p.authorURL(microdataURL(u))
	}
	p.addAuthor(a)
}

// hasItemprop reports whether n's itemprop token list names prop.
func hasItemprop(n *html.Node, prop string) bool {
	for _, tok := range strings.Fields(attr(n, "itemprop")) {
		if strings.EqualFold(tok, prop) {
			return true
		}
	}
	return false
}

// itemProperty finds the first descendant of item carrying itemprop=prop that
// belongs to item itself: a nested itemscope starts another item, whose
// properties (a Person's employer's name) are not item's.
func itemProperty(item *html.Node, prop string) *html.Node {
	for c := item.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if hasItemprop(c, prop) {
			return c
		}
		if hasAttr(c, "itemscope") {
			continue
		}
		if found := itemProperty(c, prop); found != nil {
			return found
		}
	}
	return nil
}

// microdataText is a property's text value: a <meta>'s content, else the
// element's collapsed text, capped.
func microdataText(n *html.Node) string {
	if n.Data == "meta" {
		return collapseSpace(attr(n, "content"))
	}
	return elementText(n)
}

// microdataURL is a URL property's value, from the attribute microdata reads
// it from for the element (href, src or content), else its text.
func microdataURL(n *html.Node) string {
	for _, name := range []string{"href", "src", "content"} {
		if v := attr(n, name); v != "" {
			return v
		}
	}
	return elementText(n)
}

// elementText is an element's collapsed text, capped at maxAuthorText.
func elementText(n *html.Node) string {
	text := collapseSpace(subtreeText(n))
	if r := []rune(text); len(r) > maxAuthorText {
		text = strings.TrimSpace(string(r[:maxAuthorText]))
	}
	return text
}

// bylineWords are the class/id words that mark an element as a byline.
var bylineWords = map[string]bool{"author": true, "authors": true, "byline": true}

// byline records a visible element whose class or id holds a byline word, as
// its collapsed text, with a URL when it links exactly one place (its own
// href, or its only link: "By Jane on March 3" cannot say which is the
// author's). Site furniture is skipped — nav, the site footer — as are the
// document's own html/body/head, which a CMS classes "author" on an author
// archive. Nested bylines each count, like nested data-nosnippet elements.
func (p *parser) byline(n *html.Node, path string) {
	switch n.Data {
	case "html", "head", "body":
		return
	}
	if inHead(path) || !hasBylineWord(n) || inSiteFurniture(path) || hidden(n) {
		return
	}
	text := elementText(n)
	if text == "" {
		return
	}
	a := Author{Source: AuthorByline, Name: text}
	if href := attr(n, "href"); href != "" {
		a.URL = p.authorURL(href)
	} else if only := onlyLink(n); only != nil {
		a.URL = p.authorURL(attr(only, "href"))
	}
	p.addAuthor(a)
}

// hasBylineWord reports whether n's class or id holds a byline word.
func hasBylineWord(n *html.Node) bool {
	for _, name := range []string{"class", "id"} {
		for _, w := range nameWords(attr(n, name)) {
			if bylineWords[w] {
				return true
			}
		}
	}
	return false
}

// nameWords splits a class or id value into lowercase words at every
// character that is not a letter or digit and at each lower-to-upper case
// change, so post-author, author_name, c-byline__name and postAuthor all hold
// the word, and authority or coauthored do not.
func nameWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	var prev rune
	for _, r := range s {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
		prev = r
	}
	flush()
	return words
}

// inSiteFurniture reports whether an element path runs through a <nav>, or a
// <footer> outside any <article>: the site's footer. An article's own footer
// is where HTML puts its author, so it is content.
func inSiteFurniture(path string) bool {
	article := false
	for seg := range strings.SplitSeq(path, "/") {
		switch seg {
		case "nav":
			return true
		case "article":
			article = true
		case "footer":
			if !article {
				return true
			}
		}
	}
	return false
}

// hidden reports whether n or an ancestor is hidden from the reader: the
// hidden attribute, aria-hidden="true", or an inline display:none /
// visibility:hidden. Stylesheets are not evaluated; with rendering on, the
// rendered DOM is what is read.
func hidden(n *html.Node) bool {
	for cur := n; cur != nil && cur.Type == html.ElementNode; cur = cur.Parent {
		if hasAttr(cur, "hidden") || strings.EqualFold(strings.TrimSpace(attr(cur, "aria-hidden")), "true") {
			return true
		}
		style := strings.ToLower(strings.Join(strings.Fields(attr(cur, "style")), ""))
		if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
			return true
		}
	}
	return false
}

// onlyLink returns n's one descendant <a href>, or nil when it has none or
// several.
func onlyLink(n *html.Node) *html.Node {
	var found *html.Node
	count := 0
	var visit func(*html.Node)
	visit = func(c *html.Node) {
		for ; c != nil && count < 2; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "a" && attr(c, "href") != "" {
				found = c
				count++
			}
			visit(c.FirstChild)
		}
	}
	visit(n.FirstChild)
	if count != 1 {
		return nil
	}
	return found
}
