// Package indexability implements the indexability state machine
// (DESIGN.md §5.4): every URL is Indexable or Non-Indexable with the first
// matching reason, in fixed precedence order.
package indexability

import (
	"slices"
	"strings"

	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// Reasons, in evaluation order.
const (
	BlockedByRobots = "Blocked by Robots.txt"
	NoResponse      = "No Response"
	ClientError     = "Client Error"
	ServerError     = "Server Error"
	Redirected      = "Redirected"
	Noindex         = "Noindex"
	Canonicalised   = "Canonicalised"
)

// Input is everything the verdict depends on.
type Input struct {
	PageURL       string
	StatusCode    int
	FetchError    string
	RobotsBlocked bool

	MetaRefreshURL string // resolved target; may equal the page itself
	MetaRobots     []string
	XRobotsTag     []string
	Canonicals     []string // HTML first, then HTTP; absolute URLs

	RobotsUserAgent           string // for UA-scoped X-Robots-Tag values
	RespectSelfRefMetaRefresh bool   // advanced.respect_self_referencing_meta_refresh
	Opts                      urlutil.Options
}

// Result is the verdict.
type Result struct {
	Indexable bool
	Status    string // reason when non-indexable, "" otherwise
}

func Evaluate(in Input) Result {
	if in.RobotsBlocked {
		return non(BlockedByRobots)
	}
	if in.FetchError != "" || in.StatusCode == 0 {
		return non(NoResponse)
	}
	if in.StatusCode >= 500 {
		return non(ServerError)
	}
	if in.StatusCode >= 400 {
		return non(ClientError)
	}
	if in.StatusCode >= 300 {
		return non(Redirected)
	}
	if in.MetaRefreshURL != "" {
		self := normalize(in.PageURL, in.Opts)
		if normalize(in.MetaRefreshURL, in.Opts) != self || in.RespectSelfRefMetaRefresh {
			return non(Redirected)
		}
	}
	if hasNoindex(in.MetaRobots, "") || hasNoindex(in.XRobotsTag, in.RobotsUserAgent) {
		return non(Noindex)
	}
	if canonical := firstNonEmpty(in.Canonicals); canonical != "" {
		if normalize(canonical, in.Opts) != normalize(in.PageURL, in.Opts) {
			return non(Canonicalised)
		}
	}
	return Result{Indexable: true}
}

func non(reason string) Result { return Result{Status: reason} }

func normalize(u string, opts urlutil.Options) string {
	if norm, err := urlutil.Normalize(u, opts); err == nil {
		return norm
	}
	return u
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// knownDirectives distinguishes "directive: argument" values (like
// unavailable_after: <date>) from UA-scoped values ("googlebot: noindex").
var knownDirectives = []string{
	"all", "noindex", "nofollow", "none", "noarchive", "nosnippet",
	"max-snippet", "max-image-preview", "max-video-preview", "notranslate",
	"noimageindex", "unavailable_after", "indexifembedded", "noodp", "noydir",
}

// hasNoindex checks directive values for noindex/none. X-Robots-Tag values
// may be scoped to a user-agent token ("otherbot: noindex"); scoped values
// only apply when the token matches our robots user-agent.
func hasNoindex(values []string, robotsUA string) bool {
	for _, value := range values {
		if scope, rest, ok := splitUAScope(value); ok {
			if !strings.EqualFold(scope, robotsUA) {
				continue
			}
			value = rest
		}
		for directive := range strings.SplitSeq(value, ",") {
			directive = strings.ToLower(strings.TrimSpace(directive))
			if directive == "noindex" || directive == "none" {
				return true
			}
		}
	}
	return false
}

func splitUAScope(value string) (scope, rest string, ok bool) {
	head, tail, found := strings.Cut(value, ":")
	if !found {
		return "", "", false
	}
	head = strings.TrimSpace(head)
	if strings.ContainsAny(head, " ,") || slices.Contains(knownDirectives, strings.ToLower(head)) {
		return "", "", false
	}
	return head, tail, true
}
