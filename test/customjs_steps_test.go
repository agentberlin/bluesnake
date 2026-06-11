package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registerCustomJSSteps wires the custom JavaScript snippet steps used by the
// @chrome-tagged features/custom_js.feature.
func (w *world) registerCustomJSSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a custom JS (extraction|action) snippet "([^"]*)" containing "([^"]*)"$`, w.customJSSnippet)
}

// customJSSnippet writes the snippet source into the scenario dir and folds it
// into a single custom_js crawl override: config.Set replaces the whole list
// on every application, so successive snippets must accumulate into one
// override entry to coexist.
func (w *world) customJSSnippet(typ, name, source string) error {
	file := filepath.Join(w.tmpDir, name+".js")
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		return err
	}
	entry := fmt.Sprintf("{name: '%s', type: %s, file: '%s'}", name, typ, file)
	for i, o := range w.crawlOverride {
		if rest, ok := strings.CutPrefix(o, "custom_js=["); ok {
			w.crawlOverride[i] = "custom_js=[" + strings.TrimSuffix(rest, "]") + ", " + entry + "]"
			return nil
		}
	}
	w.crawlOverride = append(w.crawlOverride, "custom_js=["+entry+"]")
	return nil
}
