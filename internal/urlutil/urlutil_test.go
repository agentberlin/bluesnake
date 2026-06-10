package urlutil

import (
	"regexp"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		// default ports dropped
		{"https://ex.com:443/a", "https://ex.com/a"},
		{"http://ex.com:80/a", "http://ex.com/a"},
		{"http://ex.com:8080/a", "http://ex.com:8080/a"},
		// scheme+host lowercased, path case preserved
		{"HTTPS://EX.com/Path", "https://ex.com/Path"},
		{"https://Ex.Com/UPPER?Q=1", "https://ex.com/UPPER?Q=1"},
		// empty path becomes /
		{"https://ex.com", "https://ex.com/"},
		{"https://ex.com?q=1", "https://ex.com/?q=1"},
		// fragments stripped by default
		{"https://ex.com/a#section", "https://ex.com/a"},
		{"https://ex.com/a#", "https://ex.com/a"},
		// percent-encoding canonicalized, uppercase hex
		{"https://ex.com/a b", "https://ex.com/a%20b"},
		{"https://ex.com/café", "https://ex.com/caf%C3%A9"},
		{"https://ex.com/caf%c3%a9", "https://ex.com/caf%C3%A9"},
		// query preserved verbatim (order, case)
		{"https://ex.com/p?b=2&a=1", "https://ex.com/p?b=2&a=1"},
		// trailing slash significance preserved
		{"https://ex.com/a/", "https://ex.com/a/"},
		{"https://ex.com/a", "https://ex.com/a"},
		// userinfo preserved
		{"https://user:pw@ex.com/a", "https://user:pw@ex.com/a"},
	}
	for _, tt := range tests {
		got, err := Normalize(tt.in, Options{})
		if err != nil {
			t.Errorf("Normalize(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeKeepFragments(t *testing.T) {
	got, err := Normalize("https://ex.com/a#section", Options{KeepFragments: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://ex.com/a#section" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizeLowercaseHex(t *testing.T) {
	got, err := Normalize("https://ex.com/café", Options{LowercaseHex: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://ex.com/caf%c3%a9" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		"https://ex.com/café", "https://EX.com:443", "https://ex.com/a b?q=x y",
		"http://ex.com/%7Euser/", "https://ex.com/a//b", "https://ex.com/a/../b",
	}
	for _, in := range inputs {
		once, err := Normalize(in, Options{})
		if err != nil {
			t.Fatal(err)
		}
		twice, err := Normalize(once, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("not idempotent for %q: %q -> %q", in, once, twice)
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		base, href, want string
	}{
		{"https://ex.com/a/b", "c", "https://ex.com/a/c"},
		{"https://ex.com/a/b/", "c", "https://ex.com/a/b/c"},
		{"https://ex.com/a/b", "/c", "https://ex.com/c"},
		{"https://ex.com/a/b", "//cdn.ex.com/x.js", "https://cdn.ex.com/x.js"},
		{"http://ex.com/a/b", "//cdn.ex.com/x.js", "http://cdn.ex.com/x.js"},
		{"https://ex.com/a/b", "https://other.com/p?q=1", "https://other.com/p?q=1"},
		{"https://ex.com/a/b", "../up", "https://ex.com/up"},
		{"https://ex.com/a/", "./same", "https://ex.com/a/same"},
		{"https://ex.com/a", "?q=2", "https://ex.com/a?q=2"},
		{"https://ex.com/a", "#frag", "https://ex.com/a"},
		{"https://ex.com/a/b", "", "https://ex.com/a/b"},
		// whitespace around hrefs is trimmed (browsers do this)
		{"https://ex.com/", "  /spaced  ", "https://ex.com/spaced"},
		// dot segments above root clamp at root
		{"https://ex.com/a", "../../../x", "https://ex.com/x"},
	}
	for _, tt := range tests {
		got, err := Resolve(tt.base, tt.href, Options{})
		if err != nil {
			t.Errorf("Resolve(%q, %q): %v", tt.base, tt.href, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tt.base, tt.href, got, tt.want)
		}
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		in    string
		valid bool
	}{
		{"https://ex.com/ok", true},
		{"http://ex.com", true},
		{"https://localhost/x", true},
		{"https://127.0.0.1:8080/x", true},
		{"https://sub.ex.co.uk/x", true},
		{"hppts://ex.com/", false},
		{"javascript:void(0)", false},
		{"mailto:x@ex.com", false},
		{"tel:+123456", false},
		{"ftp://ex.com/file", false},
		{"https://", false},
		{"", false},
		{"not a url", false},
		{"https://example/", false}, // no TLD
	}
	for _, tt := range tests {
		if got := IsValid(tt.in); got != tt.valid {
			t.Errorf("IsValid(%q) = %v, want %v", tt.in, got, tt.valid)
		}
	}
}

func TestScopeClassify(t *testing.T) {
	t.Run("subdomain start", func(t *testing.T) {
		s, err := NewScope("https://www.ex.com/blog/", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			url  string
			want ScopeClass
		}{
			{"https://www.ex.com/blog/post-1", Internal},
			{"https://www.ex.com/about", Internal},
			{"http://www.ex.com/about", Internal}, // protocol does not change scope
			{"https://WWW.EX.com/x", Internal},
			{"https://shop.ex.com/item", External},
			{"https://ex.com/x", External},
			{"https://other.com/x", External},
		}
		for _, tt := range tests {
			if got := s.Classify(tt.url); got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.url, got, tt.want)
			}
		}
	})

	t.Run("all subdomains", func(t *testing.T) {
		s, err := NewScope("https://www.ex.com/", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		for url, want := range map[string]ScopeClass{
			"https://shop.ex.com/item": Internal,
			"https://ex.com/x":         Internal,
			"https://exother.com/x":    External,
			"https://notex.com/x":      External,
		} {
			if got := s.Classify(url); got != want {
				t.Errorf("Classify(%q) = %v, want %v", url, got, want)
			}
		}
	})

	t.Run("bare root domain start implies all subdomains", func(t *testing.T) {
		s, err := NewScope("https://ex.com/", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("https://shop.ex.com/item"); got != Internal {
			t.Errorf("bare-domain start must make subdomains internal, got %v", got)
		}
	})

	t.Run("multi-label public suffix", func(t *testing.T) {
		s, err := NewScope("https://www.ex.co.uk/", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("https://shop.ex.co.uk/x"); got != Internal {
			t.Errorf("co.uk sibling subdomain must be internal, got %v", got)
		}
		if got := s.Classify("https://other.co.uk/x"); got != External {
			t.Errorf("other co.uk domain must be external, got %v", got)
		}
	})

	t.Run("explicit ports split authorities", func(t *testing.T) {
		s, err := NewScope("http://127.0.0.1:8080/", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("http://127.0.0.1:8080/x"); got != Internal {
			t.Errorf("same host:port must be internal, got %v", got)
		}
		if got := s.Classify("http://127.0.0.1:9090/x"); got != External {
			t.Errorf("different port must be external, got %v", got)
		}
	})

	t.Run("default ports do not split http vs https", func(t *testing.T) {
		s, err := NewScope("https://www.ex.com/", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("http://www.ex.com:80/x"); got != Internal {
			t.Errorf("default-port http must stay internal, got %v", got)
		}
	})

	t.Run("CDNs are internal", func(t *testing.T) {
		s, err := NewScope("https://www.ex.com/", false, []string{"assets.cdn.net", "img.cdn.io/ex/"})
		if err != nil {
			t.Fatal(err)
		}
		for url, want := range map[string]ScopeClass{
			"https://assets.cdn.net/img.png": Internal,
			"https://img.cdn.io/ex/a.png":    Internal,
			"https://img.cdn.io/other/a.png": External, // outside the CDN subfolder scope
			"https://other.cdn.net/x":        External,
		} {
			if got := s.Classify(url); got != want {
				t.Errorf("Classify(%q) = %v, want %v", url, got, want)
			}
		}
	})
}

func TestFolderDepth(t *testing.T) {
	tests := []struct {
		url  string
		want int
	}{
		{"https://ex.com/", 0},
		{"https://ex.com", 0},
		{"https://ex.com/page", 0},
		{"https://ex.com/a/", 1},
		{"https://ex.com/a/page", 1},
		{"https://ex.com/a/b/c/", 3},
		{"https://ex.com/a/b/c/page.html", 3},
		{"https://ex.com/a/b/c/?q=1", 3},
	}
	for _, tt := range tests {
		if got := FolderDepth(tt.url); got != tt.want {
			t.Errorf("FolderDepth(%q) = %d, want %d", tt.url, got, tt.want)
		}
	}
}

func TestPathType(t *testing.T) {
	tests := []struct {
		href string
		want PathType
	}{
		{"https://ex.com/a", Absolute},
		{"HTTP://ex.com/a", Absolute},
		{"//cdn.ex.com/a", ProtocolRelative},
		{"/a/b", RootRelative},
		{"a/b", PathRelative},
		{"../a", PathRelative},
		{"./a", PathRelative},
		{"?q=1", PathRelative},
	}
	for _, tt := range tests {
		if got := ClassifyPathType(tt.href); got != tt.want {
			t.Errorf("ClassifyPathType(%q) = %v, want %v", tt.href, got, tt.want)
		}
	}
}

func TestRewriter(t *testing.T) {
	t.Run("remove params", func(t *testing.T) {
		rw := NewRewriter([]string{"utm_source", "utm_medium", "sessionid"}, nil, false, Options{})
		tests := []struct{ in, want string }{
			{"https://ex.com/p?utm_source=x&id=2&sessionid=abc", "https://ex.com/p?id=2"},
			{"https://ex.com/p?q=1", "https://ex.com/p?q=1"},
			{"https://ex.com/p?utm_source=x", "https://ex.com/p"},
			{"https://ex.com/p", "https://ex.com/p"},
			// parameter name matching is case-insensitive (defensive: servers treat them differently,
			// but tracking params appear in both cases in the wild)
			{"https://ex.com/p?UTM_SOURCE=x&id=2", "https://ex.com/p?id=2"},
		}
		for _, tt := range tests {
			if got := rw.Rewrite(tt.in); got != tt.want {
				t.Errorf("Rewrite(%q) = %q, want %q", tt.in, got, tt.want)
			}
		}
	})

	t.Run("regex replace in order", func(t *testing.T) {
		rw := NewRewriter(nil, []RegexReplace{
			{Pattern: regexp.MustCompile(`/old/`), Replace: "/new/"},
			{Pattern: regexp.MustCompile(`^http://`), Replace: "https://"},
		}, false, Options{})
		if got := rw.Rewrite("http://ex.com/old/page"); got != "https://ex.com/new/page" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("backreferences", func(t *testing.T) {
		rw := NewRewriter(nil, []RegexReplace{
			{Pattern: regexp.MustCompile(`/product/(\d+)/.+`), Replace: "/product/$1"},
		}, false, Options{})
		if got := rw.Rewrite("https://ex.com/product/42/blue-shirt"); got != "https://ex.com/product/42" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("lowercase", func(t *testing.T) {
		rw := NewRewriter(nil, nil, true, Options{})
		if got := rw.Rewrite("https://ex.com/Some/Path?Q=Mixed"); got != "https://ex.com/some/path?q=mixed" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("result is re-normalized", func(t *testing.T) {
		rw := NewRewriter(nil, []RegexReplace{
			{Pattern: regexp.MustCompile(`page$`), Replace: "page#frag"},
		}, false, Options{})
		if got := rw.Rewrite("https://ex.com/page"); got != "https://ex.com/page" {
			t.Errorf("fragment introduced by rewrite must be stripped, got %q", got)
		}
	})

	t.Run("nil rewriter is a no-op", func(t *testing.T) {
		rw := NewRewriter(nil, nil, false, Options{})
		if got := rw.Rewrite("https://ex.com/p?q=1"); got != "https://ex.com/p?q=1" {
			t.Errorf("got %q", got)
		}
	})
}

func TestFilter(t *testing.T) {
	re := func(p string) *regexp.Regexp { return regexp.MustCompile(p) }

	t.Run("no patterns allows all", func(t *testing.T) {
		f := NewFilter(nil, nil)
		if !f.Allowed("https://ex.com/anything") {
			t.Error("must allow")
		}
	})

	t.Run("include restricts", func(t *testing.T) {
		f := NewFilter([]*regexp.Regexp{re(`/blog/`)}, nil)
		if !f.Allowed("https://ex.com/blog/post") {
			t.Error("blog must be allowed")
		}
		if f.Allowed("https://ex.com/shop/item") {
			t.Error("shop must be denied")
		}
	})

	t.Run("includes are OR-ed", func(t *testing.T) {
		f := NewFilter([]*regexp.Regexp{re(`/blog/`), re(`/docs/`)}, nil)
		if !f.Allowed("https://ex.com/docs/x") || !f.Allowed("https://ex.com/blog/x") {
			t.Error("both includes must be allowed")
		}
		if f.Allowed("https://ex.com/shop/x") {
			t.Error("shop must be denied")
		}
	})

	t.Run("exclude wins over include", func(t *testing.T) {
		f := NewFilter([]*regexp.Regexp{re(`/blog/`)}, []*regexp.Regexp{re(`/blog/private/`)})
		if !f.Allowed("https://ex.com/blog/post") {
			t.Error("blog must be allowed")
		}
		if f.Allowed("https://ex.com/blog/private/x") {
			t.Error("private must be denied")
		}
	})

	t.Run("partial match not anchored", func(t *testing.T) {
		f := NewFilter(nil, []*regexp.Regexp{re(`\?page=`)})
		if f.Allowed("https://ex.com/list?page=2") {
			t.Error("must be denied")
		}
		if !f.Allowed("https://ex.com/list") {
			t.Error("must be allowed")
		}
	})
}

func TestQueryParamCount(t *testing.T) {
	tests := []struct {
		url  string
		want int
	}{
		{"https://ex.com/p", 0},
		{"https://ex.com/p?a=1", 1},
		{"https://ex.com/p?a=1&b=2&c=3", 3},
		{"https://ex.com/p?a", 1},
	}
	for _, tt := range tests {
		if got := QueryParamCount(tt.url); got != tt.want {
			t.Errorf("QueryParamCount(%q) = %d, want %d", tt.url, got, tt.want)
		}
	}
}

func TestHost(t *testing.T) {
	tests := []struct {
		url, want string
	}{
		{"https://ex.com/p", "ex.com"},
		{"https://Shop.EX.com:8443/p", "shop.ex.com"},
		{"https://user:pw@ex.com/p", "ex.com"},
	}
	for _, tt := range tests {
		if got := Host(tt.url); got != tt.want {
			t.Errorf("Host(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
