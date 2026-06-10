package parse

import (
	"net/http"
	"strings"

	"github.com/hhsecond/acrawler/internal/urlutil"
)

// parseHeaderFacts extracts the HTTP-level equivalents of head elements:
// X-Robots-Tag directives and Link-header canonicals/pagination/hreflang.
func parseHeaderFacts(pageURL string, header http.Header, facts *Facts, opts urlutil.Options) {
	if header == nil {
		return
	}
	facts.XRobotsTag = append(facts.XRobotsTag, header.Values("X-Robots-Tag")...)

	for _, value := range header.Values("Link") {
		for _, entry := range splitLinkHeader(value) {
			target, params := parseLinkEntry(entry)
			if target == "" {
				continue
			}
			resolved, err := urlutil.Resolve(pageURL, target, opts)
			if err != nil {
				continue
			}
			for rel := range strings.FieldsSeq(strings.ToLower(params["rel"])) {
				switch rel {
				case "canonical":
					facts.CanonicalHTTP = append(facts.CanonicalHTTP, resolved)
				case "next":
					facts.NextHTTP = append(facts.NextHTTP, resolved)
				case "prev", "previous":
					facts.PrevHTTP = append(facts.PrevHTTP, resolved)
				case "alternate":
					if lang := params["hreflang"]; lang != "" {
						facts.HreflangHTTP = append(facts.HreflangHTTP, Hreflang{Lang: lang, URL: resolved})
					}
				}
			}
		}
	}
}

// splitLinkHeader splits a Link header on top-level commas (commas inside
// <...> or quoted strings do not split).
func splitLinkHeader(value string) []string {
	var entries []string
	depth, quoted := 0, false
	start := 0
	for i, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			depth++
		case r == '>':
			depth--
		case r == ',' && depth == 0:
			entries = append(entries, value[start:i])
			start = i + 1
		}
	}
	entries = append(entries, value[start:])
	return entries
}

// parseLinkEntry parses one `<url>; param="value"; param=value` entry.
func parseLinkEntry(entry string) (target string, params map[string]string) {
	params = map[string]string{}
	parts := strings.Split(entry, ";")
	first := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(first, "<") || !strings.Contains(first, ">") {
		return "", params
	}
	target = strings.TrimSpace(first[1:strings.Index(first, ">")])
	for _, part := range parts[1:] {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.Trim(strings.TrimSpace(value), `"`)
		params[name] = value
	}
	return target, params
}
