package indexability

import "testing"

func base() Input {
	return Input{
		PageURL:                   "https://ex.com/p",
		StatusCode:                200,
		RobotsUserAgent:           "acrawler",
		RespectSelfRefMetaRefresh: true,
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Input)
		indexable bool
		status    string
	}{
		{"plain 200", nil, true, ""},
		{"robots blocked", func(in *Input) { in.RobotsBlocked = true; in.StatusCode = 0 }, false, BlockedByRobots},
		{"fetch error", func(in *Input) { in.FetchError = "timeout"; in.StatusCode = 0 }, false, NoResponse},
		{"status zero", func(in *Input) { in.StatusCode = 0 }, false, NoResponse},
		{"404", func(in *Input) { in.StatusCode = 404 }, false, ClientError},
		{"410", func(in *Input) { in.StatusCode = 410 }, false, ClientError},
		{"500", func(in *Input) { in.StatusCode = 500 }, false, ServerError},
		{"301", func(in *Input) { in.StatusCode = 301 }, false, Redirected},
		{"meta refresh other", func(in *Input) { in.MetaRefreshURL = "https://ex.com/new" }, false, Redirected},
		{"meta refresh self respected", func(in *Input) { in.MetaRefreshURL = "https://ex.com/p" }, false, Redirected},
		{"meta refresh self tolerated", func(in *Input) {
			in.MetaRefreshURL = "https://ex.com/p"
			in.RespectSelfRefMetaRefresh = false
		}, true, ""},
		{"meta noindex", func(in *Input) { in.MetaRobots = []string{"noindex, follow"} }, false, Noindex},
		{"meta none", func(in *Input) { in.MetaRobots = []string{"none"} }, false, Noindex},
		{"x-robots noindex", func(in *Input) { in.XRobotsTag = []string{"noindex"} }, false, Noindex},
		{"x-robots scoped to us", func(in *Input) { in.XRobotsTag = []string{"acrawler: noindex"} }, false, Noindex},
		{"x-robots scoped to other", func(in *Input) { in.XRobotsTag = []string{"otherbot: noindex"} }, true, ""},
		{"x-robots unavailable_after is not a scope", func(in *Input) {
			in.XRobotsTag = []string{"unavailable_after: 25 Jun 2030"}
		}, true, ""},
		{"canonicalised", func(in *Input) { in.Canonicals = []string{"https://ex.com/main"} }, false, Canonicalised},
		{"self canonical", func(in *Input) { in.Canonicals = []string{"https://ex.com/p"} }, true, ""},
		{"self canonical needing normalization", func(in *Input) {
			in.Canonicals = []string{"https://EX.com:443/p"}
		}, true, ""},
		{"noindex beats canonical", func(in *Input) {
			in.MetaRobots = []string{"noindex"}
			in.Canonicals = []string{"https://ex.com/other"}
		}, false, Noindex},
		{"blocked beats everything", func(in *Input) {
			in.RobotsBlocked = true
			in.StatusCode = 500
			in.MetaRobots = []string{"noindex"}
		}, false, BlockedByRobots},
		{"noarchive alone is fine", func(in *Input) { in.MetaRobots = []string{"noarchive, nosnippet"} }, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base()
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			got := Evaluate(in)
			if got.Indexable != tt.indexable || got.Status != tt.status {
				t.Errorf("Evaluate = %+v, want indexable=%v status=%q", got, tt.indexable, tt.status)
			}
		})
	}
}
