package structured

import (
	"bufio"
	_ "embed"
	"slices"
	"strings"
	"sync"
)

//go:generate go run gen_hierarchy.go

// schemaorg_hierarchy.txt is the objective schema.org type IS-A graph (one
// `child<TAB>parent[,parent...]` line per type). It carries no curation; the
// curated rich-result root set is `requirements` in structured.go, and a seen
// type resolves to its most-specific curated ancestor at validation time — so a
// `Restaurant` (IS-A `LocalBusiness`) validates against the LocalBusiness rules
// without LocalBusiness needing a per-subtype alias. Matches Google's "use the
// most specific applicable type" guidance.
//
//go:embed schemaorg_hierarchy.txt
var hierarchyData string

// inheritanceExclusions suppress a (subtree → curated root) inheritance edge:
// every type at or below `subtree` stops resolving to `root`. Each is grounded
// in Google's current rich-results docs — the subtype belongs to a *different*
// (or retired) feature, so inheriting the parent's rules would false-flag valid
// markup. A directly-curated type keeps its own rules regardless (exclusions
// only gate inherited resolution).
var inheritanceExclusions = []struct{ subtree, root string }{
	// "Car isn't supported automatically as a subtype of Product" (Google); the
	// dedicated Vehicle-listing rich result was deprecated 2025-06-12 — treat
	// the whole Vehicle subtree like HowTo and don't validate it as Product.
	{"Vehicle", "Product"},
	// A bare VideoGame yields no Software App rich result; OperatingSystem /
	// RuntimePlatform are informational metadata (a distro, the JVM) that
	// legitimately have no `offers`, so inheriting required[offers] over-errors.
	{"VideoGame", "SoftwareApplication"},
	{"OperatingSystem", "SoftwareApplication"},
	{"RuntimePlatform", "SoftwareApplication"},
	// ClaimReview (Fact Check) and MediaReview (media-authenticity) are Google's
	// own separate features (required claimReviewed/reviewRating/url, NOT author);
	// EmployerReview/EmployerAggregateRating are the Employer Rating feature.
	// Inheriting Review/AggregateRating[required author/ratingValue] false-errors
	// them. CriticReview/UserReview are deliberately NOT excluded — author is
	// genuinely required there.
	{"ClaimReview", "Review"},
	{"MediaReview", "Review"},
	{"EmployerReview", "Review"},
	{"EmployerAggregateRating", "AggregateRating"},
	// AggregateRating IS-A Rating, so adding Rating as a curated root would let
	// EmployerAggregateRating (the Employer Rating feature, not a star rating)
	// fall back to Rating[required ratingValue] once its AggregateRating edge is
	// suppressed above — re-introducing the very false error that suppression
	// prevents. Exclude the Rating root too so it validates against neither.
	{"EmployerAggregateRating", "Rating"},
	// ReviewNewsArticle IS-A both NewsArticle and Review (the only incomparable
	// curated tie in the vocabulary); it is fundamentally a news article, so
	// suppress the Review edge — it validates as NewsArticle (recommended-only).
	{"ReviewNewsArticle", "Review"},
	// The UserInteraction telemetry family (UserComments, UserLikes, …) IS-A
	// Event in schema.org but they are interaction counters, not scheduled
	// real-world events — inheriting Event[name,startDate,location] false-errors.
	{"UserInteraction", "Event"},
}

var (
	hierarchyOnce sync.Once
	parentsOf     map[string][]string        // child -> direct parents
	ancestorsOf   map[string]map[string]bool // type -> transitive ancestors (excl. self)
	excludeRoots  map[string]map[string]bool // type -> curated roots its inheritance is suppressed for
	resolvedRoot  map[string]string          // type -> curated root to validate against
)

func initHierarchy() {
	parentsOf = parseHierarchy(hierarchyData)
	ancestorsOf = transitiveAncestors(parentsOf)

	all := map[string]bool{}
	for c, ps := range parentsOf {
		all[c] = true
		for _, p := range ps {
			all[p] = true
		}
	}

	// Expand each (subtree → root) exclusion to every descendant of the subtree.
	excludeRoots = map[string]map[string]bool{}
	for _, ex := range inheritanceExclusions {
		for t := range all {
			if t == ex.subtree || ancestorsOf[t][ex.subtree] {
				if excludeRoots[t] == nil {
					excludeRoots[t] = map[string]bool{}
				}
				excludeRoots[t][ex.root] = true
			}
		}
	}

	resolvedRoot = make(map[string]string, len(all))
	for t := range all {
		resolvedRoot[t] = computeRoot(t)
	}
}

// parseHierarchy reads the embedded child→parent graph; comment/blank lines are
// skipped. Never panics — a malformed/empty file yields an empty graph, which
// degrades resolution to today's leaf-exact behaviour rather than breaking.
func parseHierarchy(data string) map[string][]string {
	out := map[string][]string{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		child, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		out[child] = strings.Split(rest, ",")
	}
	return out
}

// transitiveAncestors computes each type's full ancestor set (excluding itself)
// via memoized DFS. Cycle-safe: a back-edge (schema.org is acyclic, but be
// defensive) is broken by an in-progress guard so it terminates instead of
// looping forever.
func transitiveAncestors(parents map[string][]string) map[string]map[string]bool {
	anc := make(map[string]map[string]bool, len(parents))
	inProgress := map[string]bool{}
	var walk func(string) map[string]bool
	walk = func(t string) map[string]bool {
		if a, ok := anc[t]; ok {
			return a
		}
		if inProgress[t] {
			return map[string]bool{} // cycle guard
		}
		inProgress[t] = true
		a := map[string]bool{}
		for _, p := range parents[t] {
			a[p] = true
			for pa := range walk(p) {
				a[pa] = true
			}
		}
		anc[t] = a
		delete(inProgress, t)
		return a
	}
	for t := range parents {
		walk(t)
	}
	return anc
}

// computeRoot returns the curated rich-result root that governs type t, or ""
// when t is under no curated type (after exclusions). A directly-curated type
// resolves to itself (short-circuits — nothing is more specific than the type
// itself). Otherwise the most-specific curated ancestor wins: the candidate that
// is a subtype of every other curated ancestor (e.g. a `Hospital` is curated-
// ancestored by both LocalBusiness and Organization, and LocalBusiness IS-A
// Organization, so LocalBusiness wins). Incomparable candidates fall back to
// alphabetical for determinism (after exclusions, no such case exists).
func computeRoot(t string) string {
	if _, ok := requirements[t]; ok {
		return t
	}
	suppressed := excludeRoots[t]
	var cur []string
	for a := range ancestorsOf[t] {
		if _, ok := requirements[a]; ok && !suppressed[a] {
			cur = append(cur, a)
		}
	}
	switch len(cur) {
	case 0:
		return ""
	case 1:
		return cur[0]
	}
	slices.Sort(cur) // deterministic incomparable fallback
	for _, c := range cur {
		belowAll := true
		for _, c2 := range cur {
			if c != c2 && !ancestorsOf[c][c2] { // c2 must be an ancestor of c
				belowAll = false
				break
			}
		}
		if belowAll {
			return c
		}
	}
	return cur[0]
}

// resolveType maps a seen schema.org @type to the curated root that validates
// it ("" = not a rich-result candidate, validate nothing).
func resolveType(t string) string {
	hierarchyOnce.Do(initHierarchy)
	if _, ok := requirements[t]; ok {
		return t
	}
	return resolvedRoot[t]
}

// mostSpecific drops any type that is a proper ancestor of another type in the
// set — used to collapse a node's resolved roots so redundant supertypes
// (Article behind NewsArticle, Organization behind LocalBusiness) validate once.
// Unrelated types (Product vs SoftwareApplication) are all kept.
func mostSpecific(types []string) []string {
	hierarchyOnce.Do(initHierarchy)
	var out []string
	for _, t := range types {
		dominated := false
		for _, t2 := range types {
			if t != t2 && ancestorsOf[t2][t] { // t is an ancestor of t2 ⇒ t2 is more specific
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, t)
		}
	}
	return out
}
