package acceptance

// Steps for features/progress.feature. The command runs with stdout and
// stderr on separate pipes (runCLIApart), and stderr is decoded line by line
// by wire field name, the way a headless consumer parses the feed, so a
// scenario fails on a changed contract rather than a changed byte.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

func (w *world) registerProgressSteps(sc *godog.ScenarioContext) {
	sc.Step(`^I run "([^"]*)" with stdout and stderr apart$`, w.runCLIApart)
	sc.Step(`^stdout contains "([^"]*)"$`, w.stdoutContains)
	sc.Step(`^stdout is empty$`, w.stdoutEmpty)
	sc.Step(`^stderr is empty$`, w.stderrEmpty)
	sc.Step(`^every stderr line is a progress record$`, w.stderrAllProgress)
	sc.Step(`^the progress records carry the crawl ID printed on stdout$`, w.progressCrawlIDOnStdout)
	sc.Step(`^the (first|last) progress record has "([^"]*)" equal to "([^"]*)"$`, w.progressRecordField)
	sc.Step(`^a mid-crawl progress record reports at least (\d+) processed$`, w.progressMidCrawl)
	sc.Step(`^stderr is whole lines with no carriage returns$`, w.stderrWholeLines)
	sc.Step(`^a stderr line other than the last contains "([^"]*)"$`, w.stderrEarlierLineContains)
	sc.Step(`^the last stderr line contains "([^"]*)"$`, w.stderrLastLineContains)
}

// stderrLines splits stderr into its lines.
func (w *world) stderrLines() []string {
	return strings.Split(strings.TrimSuffix(w.stderr, "\n"), "\n")
}

func (w *world) stderrWholeLines() error {
	if w.stderr == "" || strings.Contains(w.stderr, "\r") || !strings.HasSuffix(w.stderr, "\n") {
		return fmt.Errorf("stderr is not newline-terminated lines without carriage returns:\n%q", w.stderr)
	}
	return nil
}

func (w *world) stderrEarlierLineContains(substr string) error {
	lines := w.stderrLines()
	for _, l := range lines[:len(lines)-1] {
		if strings.Contains(l, substr) {
			return nil
		}
	}
	return fmt.Errorf("no stderr line before the last contains %q:\n%s", substr, w.stderr)
}

func (w *world) stderrLastLineContains(substr string) error {
	lines := w.stderrLines()
	if last := lines[len(lines)-1]; !strings.Contains(last, substr) {
		return fmt.Errorf("last stderr line %q does not contain %q", last, substr)
	}
	return nil
}

func (w *world) stdoutContains(substr string) error {
	if !strings.Contains(w.stdout, substr) {
		return fmt.Errorf("stdout does not contain %q:\n%s", substr, w.stdout)
	}
	return nil
}

func (w *world) stdoutEmpty() error {
	if w.stdout != "" {
		return fmt.Errorf("stdout is not empty:\n%s", w.stdout)
	}
	return nil
}

func (w *world) stderrEmpty() error {
	if w.stderr != "" {
		return fmt.Errorf("stderr is not empty:\n%s", w.stderr)
	}
	return nil
}

// progressRecords decodes stderr as the feed: newline-terminated lines, no
// carriage returns, each one a JSON object of type "progress".
func (w *world) progressRecords() ([]map[string]any, error) {
	if w.stderr == "" {
		return nil, fmt.Errorf("stderr is empty; stdout:\n%s", w.stdout)
	}
	if strings.Contains(w.stderr, "\r") {
		return nil, fmt.Errorf("stderr contains a carriage return:\n%q", w.stderr)
	}
	if !strings.HasSuffix(w.stderr, "\n") {
		return nil, fmt.Errorf("stderr does not end with a newline:\n%q", w.stderr)
	}
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(w.stderr, "\n"), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("stderr line is not a JSON object: %q", line)
		}
		if rec["type"] != "progress" {
			return nil, fmt.Errorf("stderr record is not of type progress: %s", line)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

func (w *world) stderrAllProgress() error {
	_, err := w.progressRecords()
	return err
}

func (w *world) progressCrawlIDOnStdout() error {
	m := regexp.MustCompile(`(?m)^Crawl ID: (\S+)$`).FindStringSubmatch(w.stdout)
	if m == nil {
		return fmt.Errorf("no Crawl ID line on stdout:\n%s", w.stdout)
	}
	recs, err := w.progressRecords()
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r["crawl_id"] != m[1] {
			return fmt.Errorf("record crawl_id %v, stdout Crawl ID %s", r["crawl_id"], m[1])
		}
	}
	return nil
}

// progressRecordField compares a field's decoded value in its plain text form
// (JSON numbers print without a fraction when whole: 3, 0.25).
func (w *world) progressRecordField(which, key, want string) error {
	recs, err := w.progressRecords()
	if err != nil {
		return err
	}
	rec := recs[0]
	if which == "last" {
		rec = recs[len(recs)-1]
	}
	v, ok := rec[key]
	if !ok {
		return fmt.Errorf("%s progress record has no %q: %v", which, key, rec)
	}
	if got := fmt.Sprint(v); got != want {
		return fmt.Errorf("%s progress record %q = %s, want %s\nstderr:\n%s", which, key, got, want, w.stderr)
	}
	return nil
}

// progressMidCrawl finds a record between the start and final records that
// reports the crawl running with pages already processed.
func (w *world) progressMidCrawl(min int) error {
	recs, err := w.progressRecords()
	if err != nil {
		return err
	}
	for i := 1; i < len(recs)-1; i++ {
		if p, _ := recs[i]["processed"].(float64); recs[i]["state"] == "running" && int(p) >= min {
			return nil
		}
	}
	return fmt.Errorf("no mid-crawl running record with processed >= %d:\n%s", min, w.stderr)
}
