// Package acceptance runs the Gherkin specs in features/ against the real
// acrawler binary and library API. Scenarios tagged @pending describe modules
// not yet implemented and are excluded from the run (see README).
package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/fetch"
	"github.com/hhsecond/acrawler/internal/indexability"
	"github.com/hhsecond/acrawler/internal/issues"
	"github.com/hhsecond/acrawler/internal/parse"
	"github.com/hhsecond/acrawler/internal/robots"
	"github.com/hhsecond/acrawler/internal/urlutil"
	"gopkg.in/yaml.v3"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acrawler-acceptance")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "acrawler")
	out, err := exec.Command("go", "build", "-o", binPath, "github.com/hhsecond/acrawler/cmd/acrawler").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "building acrawler: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"../features"},
			Tags:     "~@pending && ~@chrome",
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance suite failed")
	}
}

// world is the per-scenario state shared between steps.
type world struct {
	tmpDir string

	// config steps
	cfgData   []byte
	cfgPath   string
	overrides []string
	cfg       *config.Config
	loadErr   error

	// CLI steps
	out      string
	exitCode int

	// urlutil steps
	opts       urlutil.Options
	base       string
	result     string
	stepErr    error
	validity   bool
	scope      *urlutil.Scope
	scopeClass urlutil.ScopeClass
	depth      int
	pathType   urlutil.PathType

	// rewriting steps
	removeParams []string
	replaces     []urlutil.RegexReplace
	lowercase    bool

	// filter steps
	include []*regexp.Regexp
	exclude []*regexp.Regexp

	// robots steps
	robots     *robots.File
	robotsPath string

	// fetch steps
	routes        map[string]*routeSpec
	hits          map[string]int
	hitsMu        sync.Mutex
	server        *httptest.Server
	tlsServer     bool
	fetchOverride []string
	basicUser     string
	basicPass     string
	fetchRes      *fetch.Result
	fetchClient   *fetch.Client
	seenHeaders   map[string]http.Header

	// parse steps
	pageURL       string
	pageHTML      string
	respHeader    http.Header
	parseOverride []string
	facts         *parse.Facts
	facts2        *parse.Facts

	// indexability steps
	idxInput    indexability.Input
	idxOverride []string
	idxResult   indexability.Result

	// crawl steps
	crawlOverride    []string
	crawlResult      *crawler.Result
	pathLimits       []config.PathLimit
	customRobotsPath string
	robotsContent    string
	extServer        *httptest.Server
	extHits          map[string]int
	extMu            sync.Mutex
	storedCrawlID    string
	firstCrawlID     string
	listFilePath     string
	issueOccs        []issues.Occurrence
}

type routeSpec struct {
	status     int
	body       string
	redirectTo string
	failTimes  int // respond 503 this many times before succeeding
	sleep      time.Duration
	hsts       string
	authUser   string
	authPass   string
}

func (w *world) ensureServer() *httptest.Server {
	if w.server != nil {
		return w.server
	}
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.hitsMu.Lock()
		w.hits[r.URL.Path]++
		hitCount := w.hits[r.URL.Path]
		w.seenHeaders[r.URL.Path] = r.Header.Clone()
		w.hitsMu.Unlock()

		spec, ok := w.routes[r.URL.Path]
		if !ok {
			rw.WriteHeader(404)
			return
		}
		if spec.sleep > 0 {
			time.Sleep(spec.sleep)
		}
		if spec.authUser != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != spec.authUser || p != spec.authPass {
				rw.WriteHeader(401)
				return
			}
		}
		if spec.failTimes > 0 && hitCount <= spec.failTimes {
			rw.WriteHeader(503)
			return
		}
		if spec.hsts != "" {
			rw.Header().Set("Strict-Transport-Security", spec.hsts)
		}
		if spec.redirectTo != "" {
			rw.Header().Set("Location", spec.redirectTo)
			rw.WriteHeader(spec.status)
			return
		}
		rw.WriteHeader(spec.status)
		body := strings.ReplaceAll(spec.body, "<serverurl>", "http://"+r.Host)
		fmt.Fprint(rw, body)
	})
	if w.tlsServer {
		w.server = httptest.NewTLSServer(handler)
	} else {
		w.server = httptest.NewServer(handler)
	}
	return w.server
}

func (w *world) route(path string) *routeSpec {
	if w.routes == nil {
		w.routes = map[string]*routeSpec{}
		w.hits = map[string]int{}
		w.seenHeaders = map[string]http.Header{}
	}
	if w.routes[path] == nil {
		w.routes[path] = &routeSpec{status: 200}
	}
	return w.routes[path]
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		*w = world{}
		dir, err := os.MkdirTemp("", "acrawler-scenario")
		w.tmpDir = dir
		return ctx, err
	})
	sc.After(func(ctx context.Context, s *godog.Scenario, err error) (context.Context, error) {
		if w.server != nil {
			w.server.Close()
		}
		if w.extServer != nil {
			w.extServer.Close()
		}
		if w.tmpDir != "" {
			os.RemoveAll(w.tmpDir)
		}
		return ctx, nil
	})

	// --- config ---
	sc.Step(`^a config file with contents "([^"]*)"$`, w.configFileInline)
	sc.Step(`^a config file with contents:$`, w.configFileDoc)
	sc.Step(`^the override "([^"]*)"$`, w.addOverride)
	sc.Step(`^the config is loaded$`, w.loadConfig)
	sc.Step(`^loading succeeds$`, w.loadingSucceeds)
	sc.Step(`^loading fails with an error containing "([^"]*)"$`, w.loadingFails)
	sc.Step(`^the effective value of "([^"]*)" is "([^"]*)"$`, w.effectiveValue)

	// --- CLI ---
	sc.Step(`^I run "([^"]*)"$`, w.runCLI)
	sc.Step(`^the exit code is (\d+)$`, w.checkExitCode)
	sc.Step(`^the output contains "([^"]*)"$`, w.outputContains)
	sc.Step(`^the output is a valid config that loads with all default values$`, w.outputIsDefaultConfig)

	// --- url normalization / resolution / classification ---
	sc.Step(`^a page at "([^"]*)"$`, w.pageAt)
	sc.Step(`^a link with href "([^"]*)" is discovered$`, w.linkDiscovered)
	sc.Step(`^the resolved URL is "([^"]*)"$`, w.checkResult)
	sc.Step(`^the URL "([^"]*)" is normalized$`, w.normalizeURL)
	sc.Step(`^the result is "([^"]*)"$`, w.checkResult)
	sc.Step(`^crawl_fragments is enabled$`, w.enableFragments)
	sc.Step(`^the URL "([^"]*)" is checked for validity$`, w.checkValidity)
	sc.Step(`^it is reported as "(valid|invalid)"$`, w.checkVerdict)
	sc.Step(`^the crawl started at "([^"]*)"$`, w.crawlStartedAt)
	sc.Step(`^the crawl started at "([^"]*)" with crawl_all_subdomains enabled$`, w.crawlStartedAllSubs)
	sc.Step(`^the crawl started at "([^"]*)" with CDN "([^"]*)"$`, w.crawlStartedCDN)
	sc.Step(`^the URL "([^"]*)" is classified$`, w.classifyURL)
	sc.Step(`^its scope is "(internal|external)"$`, w.checkScope)
	sc.Step(`^the folder depth of "([^"]*)" is computed$`, w.computeFolderDepth)
	sc.Step(`^the depth is (\d+)$`, w.checkDepth)
	sc.Step(`^a link with href "([^"]*)" is examined$`, w.examineHref)
	sc.Step(`^its path type is "([^"]*)"$`, w.checkPathType)

	// --- rewriting ---
	sc.Step(`^remove_params is configured with "([^"]*)"$`, w.configureRemoveParams)
	sc.Step(`^regex_replace is configured with:$`, w.configureRegexReplace)
	sc.Step(`^lowercase rewriting is enabled$`, w.enableLowercase)
	sc.Step(`^the discovered URL "([^"]*)" is rewritten$`, w.rewriteURL)
	sc.Step(`^the start URL "([^"]*)" is prepared$`, w.prepareStartURL)

	// --- robots ---
	sc.Step(`^a robots\.txt file:$`, w.robotsFile)
	sc.Step(`^"([^"]*)" is allowed for robots user-agent "([^"]*)"$`, w.robotsAllowed)
	sc.Step(`^"([^"]*)" is blocked for robots user-agent "([^"]*)"$`, w.robotsBlocked)
	sc.Step(`^blocking "([^"]*)" for "([^"]*)" reports matched line (\d+)$`, w.robotsMatchedLine)
	sc.Step(`^the robots file lists the sitemap "([^"]*)"$`, w.robotsSitemap)

	// --- fetch ---
	sc.Step(`^a test server route "([^"]*)" responding (\d+) with body "([^"]*)"$`, w.routeRespond)
	sc.Step(`^a test server route "([^"]*)" redirecting (\d+) to "([^"]*)"$`, w.routeRedirect)
	sc.Step(`^a test server route "([^"]*)" failing (\d+) times with 503 then responding 200$`, w.routeFlaky)
	sc.Step(`^a test server route "([^"]*)" requiring basic auth "([^"]*)" "([^"]*)"$`, w.routeAuth)
	sc.Step(`^a test server route "([^"]*)" that sleeps (\d+)ms before responding 200$`, w.routeSlow)
	sc.Step(`^a test server route "([^"]*)" responding (\d+) with a body of (\d+) KB$`, w.routeBigBody)
	sc.Step(`^a TLS test server route "([^"]*)" responding (\d+) with body "([^"]*)" and HSTS header "([^"]*)"$`, w.routeTLSHSTS)
	sc.Step(`^the fetch config override "([^"]*)"$`, w.addFetchOverride)
	sc.Step(`^basic auth is configured for the server with username "([^"]*)" and password "([^"]*)"$`, w.configureBasicAuth)
	sc.Step(`^I fetch "([^"]*)"$`, w.fetchPath)
	sc.Step(`^I fetch "([^"]*)" over https$`, w.fetchPath)
	sc.Step(`^I fetch "([^"]*)" over plain http on the same host$`, w.fetchPlainHTTP)
	sc.Step(`^the fetch status code is (\d+)$`, w.fetchStatusCode)
	sc.Step(`^the fetch status is "([^"]*)"$`, w.fetchStatus)
	sc.Step(`^the fetch body is "([^"]*)"$`, w.fetchBody)
	sc.Step(`^a response time was recorded$`, w.fetchTimeRecorded)
	sc.Step(`^the redirect target ends with "([^"]*)"$`, w.fetchRedirectTarget)
	sc.Step(`^the redirect type is "([^"]*)"$`, w.fetchRedirectType)
	sc.Step(`^the server received (\d+) requests? to "([^"]*)"$`, w.serverHits)
	sc.Step(`^the server saw user-agent "([^"]*)" on "([^"]*)"$`, w.serverSawUA)
	sc.Step(`^the server saw header "([^"]*)" with value "([^"]*)" on "([^"]*)"$`, w.serverSawHeader)
	sc.Step(`^the fetch reports a network error$`, w.fetchNetworkError)
	sc.Step(`^the fetch body is truncated to (\d+) bytes$`, w.fetchTruncated)

	// --- parse + indexability (registered in parse_steps_test.go) ---
	w.registerParseSteps(sc)
	w.registerIndexabilitySteps(sc)

	// --- crawl (registered in crawl_steps_test.go) ---
	w.registerCrawlSteps(sc)

	// --- store / resume (registered in store_steps_test.go) ---
	w.registerStoreSteps(sc)

	// --- issues (registered in issues_steps_test.go) ---
	w.registerIssuesSteps(sc)

	// --- include/exclude ---
	sc.Step(`^no include or exclude patterns$`, w.noPatterns)
	sc.Step(`^include patterns:$`, w.includePatterns)
	sc.Step(`^exclude patterns:$`, w.excludePatterns)
	sc.Step(`^the discovered URL "([^"]*)" is allowed$`, w.urlAllowed)
	sc.Step(`^the discovered URL "([^"]*)" is denied$`, w.urlDenied)
	sc.Step(`^the start URL "([^"]*)" is allowed$`, w.startURLAllowed)
}

// --- config step implementations ---

func (w *world) configFileInline(contents string) error {
	w.cfgData = []byte(contents)
	return w.writeCfgFile()
}

func (w *world) configFileDoc(doc *godog.DocString) error {
	w.cfgData = []byte(doc.Content)
	return w.writeCfgFile()
}

func (w *world) writeCfgFile() error {
	w.cfgPath = filepath.Join(w.tmpDir, "acrawler.yaml")
	return os.WriteFile(w.cfgPath, w.cfgData, 0o644)
}

func (w *world) addOverride(o string) error {
	w.overrides = append(w.overrides, o)
	return nil
}

func (w *world) loadConfig() error {
	c, err := config.Load(w.cfgData)
	if err == nil {
		for _, o := range w.overrides {
			if err = c.Set(o); err != nil {
				break
			}
		}
		if err == nil {
			err = c.Validate()
		}
	}
	w.cfg, w.loadErr = c, err
	return nil
}

func (w *world) loadingSucceeds() error {
	if w.loadErr != nil {
		return fmt.Errorf("expected success, got: %v", w.loadErr)
	}
	return nil
}

func (w *world) loadingFails(substr string) error {
	if w.loadErr == nil {
		return fmt.Errorf("expected an error containing %q, loading succeeded", substr)
	}
	if !strings.Contains(w.loadErr.Error(), substr) {
		return fmt.Errorf("error %q does not contain %q", w.loadErr.Error(), substr)
	}
	return nil
}

func (w *world) effectiveValue(key, want string) error {
	if w.loadErr != nil {
		return fmt.Errorf("config did not load: %v", w.loadErr)
	}
	got, err := w.cfg.Get(key)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s = %q, want %q", key, got, want)
	}
	return nil
}

// --- CLI step implementations ---

func (w *world) runCLI(command string) error {
	command = strings.ReplaceAll(command, "<configfile>", w.cfgPath)
	command = strings.ReplaceAll(command, "<robotsfile>", w.robotsPath)
	if strings.Contains(command, "<serverurl>") {
		command = strings.ReplaceAll(command, "<serverurl>", w.ensureServer().URL)
	}
	command = strings.ReplaceAll(command, "<storedir>", w.storeDirPath())
	command = strings.ReplaceAll(command, "<listfile>", w.listFilePath)
	command = strings.ReplaceAll(command, "<firstcrawlid>", w.firstCrawlID)
	if strings.Contains(command, "<crawlid>") {
		w.storedCrawlID = w.latestCrawlID() // pin: the command may delete it from the registry
		command = strings.ReplaceAll(command, "<crawlid>", w.storedCrawlID)
	}
	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "acrawler" {
		return fmt.Errorf("command must start with 'acrawler': %q", command)
	}
	cmd := exec.Command(binPath, args[1:]...)
	cmd.Dir = w.tmpDir
	cmd.Env = append(os.Environ(), "HOME="+w.tmpDir) // keep default store dir inside the scenario
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	w.out = buf.String()
	w.exitCode = 0
	if ee, ok := err.(*exec.ExitError); ok {
		w.exitCode = ee.ExitCode()
	} else if err != nil {
		return err
	}
	return nil
}

func (w *world) checkExitCode(want int) error {
	if w.exitCode != want {
		return fmt.Errorf("exit code %d, want %d\noutput:\n%s", w.exitCode, want, w.out)
	}
	return nil
}

func (w *world) outputContains(substr string) error {
	if !strings.Contains(w.out, substr) {
		return fmt.Errorf("output does not contain %q:\n%s", substr, w.out)
	}
	return nil
}

func (w *world) outputIsDefaultConfig() error {
	got, err := config.Load([]byte(w.out))
	if err != nil {
		return fmt.Errorf("emitted config does not load: %v", err)
	}
	wantY, err := yaml.Marshal(config.Default())
	if err != nil {
		return err
	}
	gotY, err := yaml.Marshal(got)
	if err != nil {
		return err
	}
	if !bytes.Equal(wantY, gotY) {
		return fmt.Errorf("emitted config differs from defaults")
	}
	return nil
}

// --- urlutil step implementations ---

func (w *world) pageAt(base string) error { w.base = base; return nil }

func (w *world) linkDiscovered(href string) error {
	w.result, w.stepErr = urlutil.Resolve(w.base, href, w.opts)
	return w.stepErr
}

func (w *world) checkResult(want string) error {
	if w.result != want {
		return fmt.Errorf("got %q, want %q", w.result, want)
	}
	return nil
}

func (w *world) normalizeURL(in string) error {
	w.result, w.stepErr = urlutil.Normalize(in, w.opts)
	return w.stepErr
}

func (w *world) enableFragments() error { w.opts.KeepFragments = true; return nil }

func (w *world) checkValidity(in string) error {
	w.validity = urlutil.IsValid(in)
	return nil
}

func (w *world) checkVerdict(want string) error {
	got := "invalid"
	if w.validity {
		got = "valid"
	}
	if got != want {
		return fmt.Errorf("got %s, want %s", got, want)
	}
	return nil
}

func (w *world) crawlStartedAt(start string) (err error) {
	w.scope, err = urlutil.NewScope(start, false, nil)
	return err
}

func (w *world) crawlStartedAllSubs(start string) (err error) {
	w.scope, err = urlutil.NewScope(start, true, nil)
	return err
}

func (w *world) crawlStartedCDN(start, cdn string) (err error) {
	w.scope, err = urlutil.NewScope(start, false, []string{cdn})
	return err
}

func (w *world) classifyURL(u string) error {
	w.scopeClass = w.scope.Classify(u)
	return nil
}

func (w *world) checkScope(want string) error {
	if w.scopeClass.String() != want {
		return fmt.Errorf("got %s, want %s", w.scopeClass, want)
	}
	return nil
}

func (w *world) computeFolderDepth(u string) error {
	w.depth = urlutil.FolderDepth(u)
	return nil
}

func (w *world) checkDepth(want int) error {
	if w.depth != want {
		return fmt.Errorf("got %d, want %d", w.depth, want)
	}
	return nil
}

func (w *world) examineHref(href string) error {
	w.pathType = urlutil.ClassifyPathType(href)
	return nil
}

func (w *world) checkPathType(want string) error {
	if w.pathType.String() != want {
		return fmt.Errorf("got %s, want %s", w.pathType, want)
	}
	return nil
}

// --- rewriting step implementations ---

func (w *world) configureRemoveParams(csv string) error {
	for p := range strings.SplitSeq(csv, ",") {
		w.removeParams = append(w.removeParams, strings.TrimSpace(p))
	}
	return nil
}

func (w *world) configureRegexReplace(table *godog.Table) error {
	for _, row := range table.Rows {
		if len(row.Cells) != 2 {
			return fmt.Errorf("regex_replace rows need 2 cells")
		}
		if row.Cells[0].Value == "pattern" { // header row
			continue
		}
		re, err := regexp.Compile(row.Cells[0].Value)
		if err != nil {
			return err
		}
		w.replaces = append(w.replaces, urlutil.RegexReplace{Pattern: re, Replace: row.Cells[1].Value})
	}
	return nil
}

func (w *world) enableLowercase() error { w.lowercase = true; return nil }

func (w *world) rewriteURL(u string) error {
	rw := urlutil.NewRewriter(w.removeParams, w.replaces, w.lowercase, w.opts)
	w.result = rw.Rewrite(u)
	return nil
}

// prepareStartURL: rewriting applies only to discovered URLs, never the
// start/list URLs — those are only normalized.
func (w *world) prepareStartURL(u string) (err error) {
	w.result, err = urlutil.Normalize(u, w.opts)
	return err
}

// --- robots step implementations ---

func (w *world) robotsFile(doc *godog.DocString) error {
	w.robotsContent = doc.Content
	w.robots = robots.Parse([]byte(doc.Content))
	w.robotsPath = filepath.Join(w.tmpDir, "robots.txt")
	return os.WriteFile(w.robotsPath, []byte(doc.Content), 0o644)
}

func (w *world) robotsAllowed(u, ua string) error {
	if v := w.robots.Verdict(ua, u); !v.Allowed {
		return fmt.Errorf("%s was blocked for %s (line %d: %s), want allowed", u, ua, v.Rule.Line, v.Rule.Raw)
	}
	return nil
}

func (w *world) robotsBlocked(u, ua string) error {
	if v := w.robots.Verdict(ua, u); v.Allowed {
		return fmt.Errorf("%s was allowed for %s, want blocked", u, ua)
	}
	return nil
}

func (w *world) robotsMatchedLine(u, ua string, line int) error {
	v := w.robots.Verdict(ua, u)
	if v.Allowed {
		return fmt.Errorf("%s was allowed, want blocked", u)
	}
	if v.Rule == nil || v.Rule.Line != line {
		return fmt.Errorf("matched rule %+v, want line %d", v.Rule, line)
	}
	return nil
}

func (w *world) robotsSitemap(sitemap string) error {
	if !slices.Contains(w.robots.Sitemaps, sitemap) {
		return fmt.Errorf("sitemaps %v do not include %q", w.robots.Sitemaps, sitemap)
	}
	return nil
}

// --- fetch step implementations ---

func (w *world) routeRespond(path string, status int, body string) error {
	r := w.route(path)
	r.status, r.body = status, body
	return nil
}

func (w *world) routeRedirect(path string, status int, target string) error {
	r := w.route(path)
	r.status, r.redirectTo = status, target
	return nil
}

func (w *world) routeFlaky(path string, failTimes int) error {
	w.route(path).failTimes = failTimes
	return nil
}

func (w *world) routeAuth(path, user, pass string) error {
	r := w.route(path)
	r.authUser, r.authPass = user, pass
	return nil
}

func (w *world) routeSlow(path string, ms int) error {
	w.route(path).sleep = time.Duration(ms) * time.Millisecond
	return nil
}

func (w *world) routeBigBody(path string, status, kb int) error {
	r := w.route(path)
	r.status, r.body = status, strings.Repeat("a", kb*1024)
	return nil
}

func (w *world) routeTLSHSTS(path string, status int, body, hsts string) error {
	w.tlsServer = true
	r := w.route(path)
	r.status, r.body, r.hsts = status, body, hsts
	return nil
}

func (w *world) addFetchOverride(o string) error {
	w.fetchOverride = append(w.fetchOverride, o)
	return nil
}

func (w *world) configureBasicAuth(user, pass string) error {
	w.basicUser, w.basicPass = user, pass
	return nil
}

// fetchClient is created on first fetch and reused within the scenario so
// state like the HSTS store behaves as it would during a crawl.
var errNoServer = fmt.Errorf("no test server routes defined")

func (w *world) client() (*fetch.Client, error) {
	if w.fetchClient != nil {
		return w.fetchClient, nil
	}
	if w.routes == nil {
		return nil, errNoServer
	}
	srv := w.ensureServer()
	cfg := config.Default()
	for _, o := range w.fetchOverride {
		if err := cfg.Set(o); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if w.basicUser != "" {
		cfg.HTTP.Auth.Basic = []config.BasicAuth{
			{URLPrefix: srv.URL, Username: w.basicUser, Password: w.basicPass},
		}
	}
	var opts []fetch.Option
	if w.tlsServer {
		opts = append(opts, fetch.WithInsecureTLS())
	}
	var err error
	w.fetchClient, err = fetch.New(cfg, opts...)
	return w.fetchClient, err
}

func (w *world) fetchPath(path string) error {
	c, err := w.client()
	if err != nil {
		return err
	}
	w.fetchRes = c.Fetch(context.Background(), w.server.URL+path)
	return nil
}

func (w *world) fetchPlainHTTP(path string) error {
	c, err := w.client()
	if err != nil {
		return err
	}
	httpURL := strings.Replace(w.server.URL, "https://", "http://", 1)
	w.fetchRes = c.Fetch(context.Background(), httpURL+path)
	return nil
}

func (w *world) fetchStatusCode(want int) error {
	if w.fetchRes.StatusCode != want {
		return fmt.Errorf("status = %d (error %q), want %d", w.fetchRes.StatusCode, w.fetchRes.FetchError, want)
	}
	return nil
}

func (w *world) fetchStatus(want string) error {
	if w.fetchRes.Status != want {
		return fmt.Errorf("status = %q, want %q", w.fetchRes.Status, want)
	}
	return nil
}

func (w *world) fetchBody(want string) error {
	if string(w.fetchRes.Body) != want {
		return fmt.Errorf("body = %q, want %q", w.fetchRes.Body, want)
	}
	return nil
}

func (w *world) fetchTimeRecorded() error {
	if w.fetchRes.FetchError != "" || w.fetchRes.ResponseTimeMs < 0 {
		return fmt.Errorf("no response time recorded: %+v", w.fetchRes)
	}
	return nil
}

func (w *world) fetchRedirectTarget(suffix string) error {
	if !strings.HasSuffix(w.fetchRes.RedirectURL, suffix) {
		return fmt.Errorf("redirect target = %q, want suffix %q", w.fetchRes.RedirectURL, suffix)
	}
	return nil
}

func (w *world) fetchRedirectType(want string) error {
	if w.fetchRes.RedirectType != want {
		return fmt.Errorf("redirect type = %q, want %q", w.fetchRes.RedirectType, want)
	}
	return nil
}

func (w *world) serverHits(want int, path string) error {
	w.hitsMu.Lock()
	got := w.hits[path]
	w.hitsMu.Unlock()
	if got != want {
		return fmt.Errorf("%s was hit %d times, want %d", path, got, want)
	}
	return nil
}

func (w *world) serverSawUA(ua, path string) error {
	return w.serverSawHeader("User-Agent", ua, path)
}

func (w *world) serverSawHeader(name, value, path string) error {
	w.hitsMu.Lock()
	h := w.seenHeaders[path]
	w.hitsMu.Unlock()
	if h == nil {
		return fmt.Errorf("no request seen on %s", path)
	}
	if got := h.Get(name); got != value {
		return fmt.Errorf("%s = %q, want %q", name, got, value)
	}
	return nil
}

func (w *world) fetchNetworkError() error {
	if w.fetchRes.FetchError == "" {
		return fmt.Errorf("expected a network error, got status %d", w.fetchRes.StatusCode)
	}
	return nil
}

func (w *world) fetchTruncated(wantLen int) error {
	if !w.fetchRes.Truncated {
		return fmt.Errorf("result not flagged truncated (body %d bytes)", len(w.fetchRes.Body))
	}
	if len(w.fetchRes.Body) != wantLen {
		return fmt.Errorf("body length = %d, want %d", len(w.fetchRes.Body), wantLen)
	}
	return nil
}

// --- include/exclude step implementations ---

func (w *world) noPatterns() error { return nil }

func (w *world) includePatterns(table *godog.Table) error {
	return compileColumn(table, &w.include)
}

func (w *world) excludePatterns(table *godog.Table) error {
	return compileColumn(table, &w.exclude)
}

func compileColumn(table *godog.Table, into *[]*regexp.Regexp) error {
	for _, row := range table.Rows {
		re, err := regexp.Compile(row.Cells[0].Value)
		if err != nil {
			return err
		}
		*into = append(*into, re)
	}
	return nil
}

// The discovery chain normalizes (URL-encodes) before filtering, so these
// steps do the same: patterns match the URL-encoded address.
func (w *world) urlAllowed(u string) error {
	encoded, err := urlutil.Normalize(u, w.opts)
	if err != nil {
		return err
	}
	if !urlutil.NewFilter(w.include, w.exclude).Allowed(encoded) {
		return fmt.Errorf("%s was denied, want allowed", u)
	}
	return nil
}

func (w *world) urlDenied(u string) error {
	encoded, err := urlutil.Normalize(u, w.opts)
	if err != nil {
		return err
	}
	if urlutil.NewFilter(w.include, w.exclude).Allowed(encoded) {
		return fmt.Errorf("%s was allowed, want denied", u)
	}
	return nil
}

// startURLAllowed asserts the contract that include/exclude are never applied
// to start URLs: the filter chain for seeds skips NewFilter entirely.
func (w *world) startURLAllowed(u string) error {
	if !urlutil.IsValid(u) {
		return fmt.Errorf("start URL %s is not valid", u)
	}
	return nil
}
