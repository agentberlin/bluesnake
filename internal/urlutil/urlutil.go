// Package urlutil implements URL resolution, normalization, scope
// classification, rewriting and include/exclude filtering — the discovery
// filter chain primitives (DESIGN.md §5.2). All functions operate on and
// return the "URL encoded address": the canonical percent-encoded form of a
// URL that is actually requested.
package urlutil

import (
	"net"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Options carries the normalization knobs from config
// (advanced.crawl_fragments, advanced.percent_encoding).
type Options struct {
	KeepFragments bool
	LowercaseHex  bool
}

var pctHex = regexp.MustCompile(`%[0-9a-fA-F]{2}`)

// Normalize canonicalizes a URL: scheme/host lowercased, default ports
// dropped, empty path becomes "/", percent-encoding canonicalized with
// uniform hex case, fragment stripped unless KeepFragments. The query string
// is preserved verbatim (order and case are significant to servers).
func Normalize(raw string, opts Options) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return normalizeURL(u, opts), nil
}

func normalizeURL(u *url.URL, opts Options) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(u.Scheme))
	b.WriteString("://")
	if u.User != nil {
		b.WriteString(u.User.String())
		b.WriteString("@")
	}
	b.WriteString(strings.ToLower(u.Hostname()))
	if port := u.Port(); port != "" && !isDefaultPort(u.Scheme, port) {
		b.WriteString(":")
		b.WriteString(port)
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	b.WriteString(canonHexCase(path, opts.LowercaseHex))
	if u.ForceQuery || u.RawQuery != "" {
		b.WriteString("?")
		b.WriteString(u.RawQuery)
	}
	if opts.KeepFragments && u.Fragment != "" {
		b.WriteString("#")
		b.WriteString(u.EscapedFragment())
	}
	return b.String()
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// canonHexCase rewrites every percent-encoded triplet to a uniform hex case.
func canonHexCase(s string, lower bool) string {
	return pctHex.ReplaceAllStringFunc(s, func(m string) string {
		if lower {
			return strings.ToLower(m)
		}
		return strings.ToUpper(m)
	})
}

// Resolve resolves an href found on basePage against it and normalizes the
// result. Hrefs are whitespace-trimmed the way browsers do.
func Resolve(base, href string, opts Options) (string, error) {
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", err
	}
	return normalizeURL(bu.ResolveReference(ref), opts), nil
}

// IsValid reports whether a URL is crawlable: http/https scheme and a host
// that is a registrable name (contains a dot), localhost, or an IP address.
func IsValid(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	return strings.Contains(host, ".") || host == "localhost" || net.ParseIP(host) != nil
}

// ScopeClass is the internal/external classification of a URL relative to
// the crawl's start URL.
type ScopeClass int

const (
	External ScopeClass = iota
	Internal
)

func (s ScopeClass) String() string {
	if s == Internal {
		return "internal"
	}
	return "external"
}

type cdnRule struct {
	authority  string
	pathPrefix string
}

// Scope classifies URLs against the start URL. Internal = same authority
// (hostname plus explicit non-default port — the protocol alone never changes
// scope, but a different port is a different site); with allSubdomains
// (explicit, or implied by starting at the bare registrable domain, mirroring
// Screaming Frog) any host under the same registrable domain on the same
// port; CDN entries ("host" or "host/path/") are also internal.
type Scope struct {
	authority     string
	port          string
	registrable   string
	allSubdomains bool
	cdns          []cdnRule
}

// authorityOf returns "hostname" or "hostname:port", dropping ports that are
// the default for the scheme (so http vs https never splits a site).
func authorityOf(u *url.URL) (authority, port string) {
	host := strings.ToLower(u.Hostname())
	p := u.Port()
	if p == "" || isDefaultPort(u.Scheme, p) {
		return host, ""
	}
	return host + ":" + p, p
}

func NewScope(start string, allSubdomains bool, cdns []string) (*Scope, error) {
	u, err := url.Parse(start)
	if err != nil {
		return nil, err
	}
	authority, port := authorityOf(u)
	host := strings.ToLower(u.Hostname())
	registrable, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		registrable = host // IPs, localhost: no registrable domain
	}
	s := &Scope{
		authority:     authority,
		port:          port,
		registrable:   registrable,
		allSubdomains: allSubdomains || host == registrable,
	}
	for _, c := range cdns {
		h, p, _ := strings.Cut(c, "/")
		rule := cdnRule{authority: strings.ToLower(h)}
		if p != "" {
			rule.pathPrefix = "/" + p
		}
		s.cdns = append(s.cdns, rule)
	}
	return s, nil
}

func (s *Scope) Classify(rawURL string) ScopeClass {
	u, err := url.Parse(rawURL)
	if err != nil {
		return External
	}
	authority, port := authorityOf(u)
	if authority == s.authority {
		return Internal
	}
	host := strings.ToLower(u.Hostname())
	if s.allSubdomains && port == s.port &&
		(host == s.registrable || strings.HasSuffix(host, "."+s.registrable)) {
		return Internal
	}
	for _, c := range s.cdns {
		if authority == c.authority && (c.pathPrefix == "" || strings.HasPrefix(u.EscapedPath(), c.pathPrefix)) {
			return Internal
		}
	}
	return External
}

// FolderDepth counts the subdirectory depth of a URL path the way Screaming
// Frog does — the number of completed (slash-terminated) path segments:
// "/" and "/page" are 0, "/a/" and "/a/page" are 1, "/a/b/c/" is 3.
func FolderDepth(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	path := u.EscapedPath()
	if path == "" {
		return 0
	}
	return strings.Count(path, "/") - 1
}

// PathType classifies how an href was written in the source document.
type PathType int

const (
	Absolute PathType = iota
	ProtocolRelative
	RootRelative
	PathRelative
)

func (p PathType) String() string {
	switch p {
	case Absolute:
		return "absolute"
	case ProtocolRelative:
		return "protocol_relative"
	case RootRelative:
		return "root_relative"
	default:
		return "path_relative"
	}
}

var schemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func ClassifyPathType(href string) PathType {
	href = strings.TrimSpace(href)
	switch {
	case strings.HasPrefix(href, "//"):
		return ProtocolRelative
	case schemePrefix.MatchString(href):
		return Absolute
	case strings.HasPrefix(href, "/"):
		return RootRelative
	default:
		return PathRelative
	}
}

// RegexReplace is one ordered URL rewriting rule (pre-compiled).
type RegexReplace struct {
	Pattern *regexp.Regexp
	Replace string
}

// Rewriter applies URL rewriting to *discovered* URLs (never start/list
// URLs): named query parameters removed, regex replacements in order,
// optional lowercasing, then re-normalization.
type Rewriter struct {
	removeParams map[string]bool
	replaces     []RegexReplace
	lowercase    bool
	opts         Options
}

func NewRewriter(removeParams []string, replaces []RegexReplace, lowercase bool, opts Options) *Rewriter {
	rw := &Rewriter{replaces: replaces, lowercase: lowercase, opts: opts}
	if len(removeParams) > 0 {
		rw.removeParams = make(map[string]bool, len(removeParams))
		for _, p := range removeParams {
			rw.removeParams[strings.ToLower(strings.TrimSpace(p))] = true
		}
	}
	return rw
}

func (rw *Rewriter) Rewrite(rawURL string) string {
	out := rawURL
	if rw.removeParams != nil {
		out = rw.stripParams(out)
	}
	for _, r := range rw.replaces {
		out = r.Pattern.ReplaceAllString(out, r.Replace)
	}
	if rw.lowercase {
		out = strings.ToLower(out)
	}
	if norm, err := Normalize(out, rw.opts); err == nil {
		out = norm
	}
	return out
}

func (rw *Rewriter) stripParams(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return rawURL
	}
	kept := make([]string, 0, 4)
	for pair := range strings.SplitSeq(u.RawQuery, "&") {
		name, _, _ := strings.Cut(pair, "=")
		if !rw.removeParams[strings.ToLower(name)] {
			kept = append(kept, pair)
		}
	}
	u.RawQuery = strings.Join(kept, "&")
	u.ForceQuery = false
	return u.String()
}

// Filter is the include/exclude gate: patterns are partial-match regexes
// evaluated against the URL-encoded address. Exclude wins; a non-empty
// include list requires at least one match.
type Filter struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

func NewFilter(include, exclude []*regexp.Regexp) *Filter {
	return &Filter{include: include, exclude: exclude}
}

func (f *Filter) Allowed(encodedURL string) bool {
	for _, re := range f.exclude {
		if re.MatchString(encodedURL) {
			return false
		}
	}
	if len(f.include) == 0 {
		return true
	}
	for _, re := range f.include {
		if re.MatchString(encodedURL) {
			return true
		}
	}
	return false
}

// QueryParamCount counts query parameters (for limits.max_query_strings).
func QueryParamCount(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return 0
	}
	n := 0
	for pair := range strings.SplitSeq(u.RawQuery, "&") {
		if pair != "" {
			n++
		}
	}
	return n
}

// Authority returns the scope authority of a URL: hostname plus explicit
// non-default port (see Scope).
func Authority(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	a, _ := authorityOf(u)
	return a
}

// Host returns the lowercase hostname of a URL (no port, no userinfo).
func Host(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
