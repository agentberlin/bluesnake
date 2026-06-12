package version

import (
	"regexp"
	"strings"
	"testing"
)

// Version must be a clean, trimmed semantic version — guards against an empty
// or garbled VERSION file (it's embedded, so a bad file ships silently).
func TestVersion(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty — internal/version/VERSION missing or blank")
	}
	if Version != strings.TrimSpace(Version) {
		t.Errorf("Version %q has surrounding whitespace", Version)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(Version) {
		t.Errorf("Version %q is not semver-shaped (want major.minor.patch)", Version)
	}
}
