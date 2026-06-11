// Package parse turns an HTML response into PageFacts: on-page elements
// (first instances + counts), directives, canonicals, pagination, hreflang,
// content metrics, head validity, and the typed link edges that feed
// discovery and the link graph (DESIGN.md §5.2 "parse" stage).
package parse

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/urlutil"
	"golang.org/x/net/html"
)

// LinkType classifies a link edge.
type LinkType string

const (
	Hyperlink       LinkType = "hyperlink"
	Image           LinkType = "image"
	CSS             LinkType = "css"
	JS              LinkType = "js"
	Media           LinkType = "media"
	SWF             LinkType = "swf"
	IFrame          LinkType = "iframe"
	Canonical       LinkType = "canonical"
	HreflangLink    LinkType = "hreflang"
	Next            LinkType = "next"
	Prev            LinkType = "prev"
	AMP             LinkType = "amp"
	MetaRefreshLink LinkType = "meta_refresh"
	MobileAlternate LinkType = "mobile_alternate"
	FormAction      LinkType = "form_action"
	Uncrawlable     LinkType = "uncrawlable"
	XHR             LinkType = "xhr" // GET XHR/fetch observed during JS rendering
)

// Link is one typed edge from the parsed page to a target URL.
type Link struct {
	Type     LinkType
	URL      string // resolved + normalized; empty for uncrawlable raw links
	Raw      string // href as written
	Anchor   string
	Alt      string
	Rel      string
	Target   string
	Nofollow bool
	PathType string
	ElemPath string
	Position string
	Lang     string // hreflang code
	Width    string // img width attribute
	Height   string // img height attribute
	Origin   string // html | rendered | xhr (JS rendering mode)
}

// Hreflang is one hreflang annotation.
type Hreflang struct {
	Lang string
	URL  string
}

// Form is one form on the page (security checks).
type Form struct {
	Action string // resolved; the page itself when the action attribute is absent
}

// HeadValidity holds the Google-parseability checks (Validation tab).
type HeadValidity struct {
	InvalidElementsInHead []string
	MissingHead           bool
	MultipleHead          bool
	MissingBody           bool
	MultipleBody          bool
	BodyBeforeHTML        bool
	HeadNotFirst          bool
}

// Facts is everything extracted from one HTML response.
type Facts struct {
	Titles            []string
	TitlesOutsideHead int

	Descriptions            []string
	DescriptionsOutsideHead int

	Keywords []string

	H1s           []string
	H2s           []string
	HeadingLevels []int // document order of h1..h6 levels

	MetaRobots []string
	XRobotsTag []string

	MetaRefresh    string // raw content attribute
	MetaRefreshURL string // resolved target ("" if none); self URL for bare delays

	CanonicalHTML        []string
	CanonicalHTTP        []string
	CanonicalOutsideHead int

	NextHTML, PrevHTML []string
	NextHTTP, PrevHTTP []string

	HreflangHTML        []Hreflang
	HreflangHTTP        []Hreflang
	HreflangOutsideHead int

	AMPLinks         []string
	MobileAlternates []string

	BaseHref string
	Lang     string
	IsAMP    bool // <html amp> or <html ⚡>

	HasViewport  bool // <meta name="viewport">
	HasCharset   bool // <meta charset> or http-equiv content-type
	HasAMPScript bool // <script src="...ampproject.org...">

	Forms []Form

	WordCount           int
	TextRatio           float64 // % of body text chars vs total page bytes
	AvgWordsPerSentence float64
	Flesch              float64
	ContentText         string

	Hash string

	Head  HeadValidity
	Links []Link
}

type parser struct {
	cfg     *config.Config
	opts    urlutil.Options
	pageURL string
	base    string
	facts   *Facts
}

// Parse never fails: malformed HTML is handled the way browsers handle it.
func Parse(pageURL string, body []byte, header http.Header, cfg *config.Config) *Facts {
	sum := md5.Sum(body)
	facts := &Facts{Hash: hex.EncodeToString(sum[:])}

	facts.Head = headChecks(body)
	parseHeaderFacts(pageURL, header, facts, urlOptions(cfg))

	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return facts
	}

	p := &parser{
		cfg:     cfg,
		opts:    urlOptions(cfg),
		pageURL: pageURL,
		facts:   facts,
	}
	p.base = findBaseHref(root)
	if p.base != "" {
		facts.BaseHref = p.base
	}
	p.walk(root, "")

	collectContentMetrics(root, body, cfg, facts)
	return facts
}

func urlOptions(cfg *config.Config) urlutil.Options {
	return urlutil.Options{
		KeepFragments: cfg.Advanced.CrawlFragments,
		LowercaseHex:  cfg.Advanced.PercentEncoding == "lower",
	}
}

func findBaseHref(root *html.Node) string {
	var base string
	var visit func(n *html.Node)
	visit = func(n *html.Node) {
		if base != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "base" {
			if href := attr(n, "href"); href != "" {
				base = href
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return base
}

func (p *parser) resolve(href string) string {
	base := p.pageURL
	if p.base != "" {
		if abs, err := urlutil.Resolve(p.pageURL, p.base, p.opts); err == nil {
			base = abs
		}
	}
	resolved, err := urlutil.Resolve(base, href, p.opts)
	if err != nil {
		return ""
	}
	return resolved
}

func (p *parser) walk(n *html.Node, path string) {
	childPath := path
	if n.Type == html.ElementNode {
		childPath = path + "/" + n.Data
		p.handleElement(n, childPath)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		p.walk(c, childPath)
	}
}

// inHead reports whether an element path is inside the <head> element
// (segment-aware: /html/body/header is not the head).
func inHead(path string) bool {
	return strings.Contains(path+"/", "/head/")
}

func (p *parser) handleElement(n *html.Node, path string) {
	f := p.facts
	switch n.Data {
	case "html":
		if lang := attr(n, "lang"); lang != "" {
			f.Lang = lang
		}
		if hasAttr(n, "amp") || hasAttr(n, "⚡") {
			f.IsAMP = true
		}
	case "title":
		if hasAncestorTag(path, "svg") {
			return
		}
		f.Titles = append(f.Titles, collapseSpace(subtreeText(n)))
		if !inHead(path) {
			f.TitlesOutsideHead++
		}
	case "meta":
		p.handleMeta(n, path)
	case "link":
		p.handleLinkElement(n, path)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		f.HeadingLevels = append(f.HeadingLevels, level)
		text := collapseSpace(subtreeText(n))
		switch n.Data {
		case "h1":
			f.H1s = append(f.H1s, text)
		case "h2":
			f.H2s = append(f.H2s, text)
		}
	case "a", "area":
		p.handleAnchor(n, path)
	case "img":
		p.handleImg(n, path)
	case "script":
		if src := attr(n, "src"); src != "" {
			if strings.Contains(src, "ampproject.org") {
				f.HasAMPScript = true
			}
			p.addLink(n, path, Link{Type: JS, Raw: src})
		}
	case "iframe":
		if src := attr(n, "src"); src != "" {
			p.addLink(n, path, Link{Type: IFrame, Raw: src})
		}
	case "video", "audio", "track":
		if src := attr(n, "src"); src != "" {
			p.addLink(n, path, Link{Type: Media, Raw: src})
		}
	case "source":
		if src := attr(n, "src"); src != "" {
			typ := Media
			if n.Parent != nil && n.Parent.Data == "picture" {
				typ = Image
			}
			p.addLink(n, path, Link{Type: typ, Raw: src})
		}
	case "embed":
		if src := attr(n, "src"); src != "" {
			p.addLink(n, path, Link{Type: SWF, Raw: src})
		}
	case "object":
		if data := attr(n, "data"); data != "" {
			p.addLink(n, path, Link{Type: SWF, Raw: data})
		}
	case "form":
		action := attr(n, "action")
		resolved := p.resolve(action) // empty action resolves to the page itself
		f.Forms = append(f.Forms, Form{Action: resolved})
		if action != "" {
			p.addLink(n, path, Link{Type: FormAction, Raw: action})
		}
	default:
		// uncrawlable: href on elements that are not hyperlink carriers
		if p.cfg.Links.Uncrawlable.Store && n.Data != "base" {
			if href := attr(n, "href"); href != "" {
				f.Links = append(f.Links, Link{
					Type: Uncrawlable, Raw: href,
					ElemPath: path, Position: p.position(path),
				})
			}
		}
	}
}

func (p *parser) handleMeta(n *html.Node, path string) {
	f := p.facts
	name := strings.ToLower(attr(n, "name"))
	content := attr(n, "content")
	switch name {
	case "description":
		f.Descriptions = append(f.Descriptions, content)
		if !inHead(path) {
			f.DescriptionsOutsideHead++
		}
	case "keywords":
		f.Keywords = append(f.Keywords, content)
	case "robots":
		f.MetaRobots = append(f.MetaRobots, content)
	case "viewport":
		f.HasViewport = true
	}
	if hasAttr(n, "charset") || strings.EqualFold(attr(n, "http-equiv"), "content-type") {
		f.HasCharset = true
	}
	if strings.EqualFold(attr(n, "http-equiv"), "refresh") && content != "" && f.MetaRefresh == "" {
		f.MetaRefresh = content
		target := metaRefreshTarget(content)
		if target == "" {
			// bare delay refreshes the page itself
			if norm, err := urlutil.Normalize(p.pageURL, p.opts); err == nil {
				f.MetaRefreshURL = norm
			}
		} else {
			f.MetaRefreshURL = p.resolve(target)
		}
		if f.MetaRefreshURL != "" {
			p.addLink(n, path, Link{Type: MetaRefreshLink, Raw: content, URL: f.MetaRefreshURL})
		}
	}
}

// metaRefreshTarget extracts the url= part of a refresh content attribute.
func metaRefreshTarget(content string) string {
	for part := range strings.SplitSeq(content, ";") {
		part = strings.TrimSpace(part)
		if v, ok := cutFold(part, "url="); ok {
			return strings.Trim(strings.TrimSpace(v), `'"`)
		}
	}
	return ""
}

func cutFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

func (p *parser) handleLinkElement(n *html.Node, path string) {
	f := p.facts
	href := attr(n, "href")
	if href == "" {
		return
	}
	rels := strings.Fields(strings.ToLower(attr(n, "rel")))
	resolved := p.resolve(href)
	for _, rel := range rels {
		switch rel {
		case "canonical":
			f.CanonicalHTML = append(f.CanonicalHTML, resolved)
			if !inHead(path) {
				f.CanonicalOutsideHead++
			}
			p.addLink(n, path, Link{Type: Canonical, Raw: href, URL: resolved})
		case "stylesheet":
			p.addLink(n, path, Link{Type: CSS, Raw: href, URL: resolved})
		case "next":
			f.NextHTML = append(f.NextHTML, resolved)
			p.addLink(n, path, Link{Type: Next, Raw: href, URL: resolved})
		case "prev", "previous":
			f.PrevHTML = append(f.PrevHTML, resolved)
			p.addLink(n, path, Link{Type: Prev, Raw: href, URL: resolved})
		case "amphtml":
			f.AMPLinks = append(f.AMPLinks, resolved)
			p.addLink(n, path, Link{Type: AMP, Raw: href, URL: resolved})
		case "alternate":
			if lang := attr(n, "hreflang"); lang != "" {
				f.HreflangHTML = append(f.HreflangHTML, Hreflang{Lang: lang, URL: resolved})
				if !inHead(path) {
					f.HreflangOutsideHead++
				}
				p.addLink(n, path, Link{Type: HreflangLink, Raw: href, URL: resolved, Lang: lang})
			} else if attr(n, "media") != "" {
				f.MobileAlternates = append(f.MobileAlternates, resolved)
				p.addLink(n, path, Link{Type: MobileAlternate, Raw: href, URL: resolved})
			}
		}
	}
}

func (p *parser) handleAnchor(n *html.Node, path string) {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" {
		return
	}
	lower := strings.ToLower(href)
	switch {
	case strings.HasPrefix(lower, "javascript:"):
		if p.cfg.Links.Uncrawlable.Store {
			p.facts.Links = append(p.facts.Links, Link{
				Type: Uncrawlable, Raw: href,
				Anchor: collapseSpace(subtreeText(n)), ElemPath: path, Position: p.position(path),
			})
		}
		return
	case strings.HasPrefix(lower, "mailto:"), strings.HasPrefix(lower, "tel:"),
		strings.HasPrefix(lower, "data:"), strings.HasPrefix(lower, "ftp:"):
		return
	}
	rel := attr(n, "rel")
	relTokens := strings.Fields(strings.ToLower(rel))
	nofollow := false
	for _, t := range relTokens {
		if t == "nofollow" || t == "sponsored" || t == "ugc" {
			nofollow = true
		}
	}
	p.addLink(n, path, Link{
		Type:     Hyperlink,
		Raw:      href,
		Anchor:   collapseSpace(subtreeText(n)),
		Alt:      firstImgAlt(n),
		Rel:      rel,
		Target:   attr(n, "target"),
		Nofollow: nofollow,
	})
}

func (p *parser) handleImg(n *html.Node, path string) {
	alt, altSet := attrOK(n, "alt")
	_ = altSet
	if src := attr(n, "src"); src != "" {
		p.addLink(n, path, Link{
			Type: Image, Raw: src, Alt: alt,
			Width: attr(n, "width"), Height: attr(n, "height"),
		})
	}
	if p.cfg.Advanced.ExtractSrcset {
		for _, cand := range parseSrcset(attr(n, "srcset")) {
			p.addLink(n, path, Link{Type: Image, Raw: cand, Alt: alt})
		}
	}
}

// parseSrcset extracts candidate URLs from a srcset attribute value.
func parseSrcset(srcset string) []string {
	var urls []string
	for cand := range strings.SplitSeq(srcset, ",") {
		fields := strings.Fields(cand)
		if len(fields) > 0 {
			urls = append(urls, fields[0])
		}
	}
	return urls
}

// addLink resolves, classifies and appends a link edge.
func (p *parser) addLink(_ *html.Node, path string, l Link) {
	if l.URL == "" && l.Raw != "" {
		l.URL = p.resolve(l.Raw)
	}
	if l.URL == "" {
		return
	}
	l.PathType = urlutil.ClassifyPathType(l.Raw).String()
	l.ElemPath = path
	l.Position = p.position(path)
	p.facts.Links = append(p.facts.Links, l)
}

// position applies the ordered link-position rules (first match wins).
func (p *parser) position(path string) string {
	if !p.cfg.StoreLinkPaths {
		return ""
	}
	for _, rule := range p.cfg.LinkPositions {
		if strings.Contains(path, rule.Match) {
			return rule.Name
		}
	}
	return ""
}

// --- small node helpers ---

func attr(n *html.Node, name string) string {
	v, _ := attrOK(n, name)
	return v
}

func attrOK(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func hasAttr(n *html.Node, name string) bool {
	_, ok := attrOK(n, name)
	return ok
}

func hasAncestorTag(path, tag string) bool {
	return strings.Contains(path+"/", "/"+tag+"/")
}

func subtreeText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}

func firstImgAlt(n *html.Node) string {
	var alt string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if alt != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			alt = attr(n, "alt")
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return alt
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
