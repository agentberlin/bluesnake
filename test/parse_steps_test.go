package acceptance

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/indexability"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/cucumber/godog"
)

func (w *world) registerParseSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a page at URL "([^"]*)" with HTML:$`, w.pageWithHTMLDoc)
	sc.Step(`^a page at URL "([^"]*)" with (?:raw )?HTML "([^"]*)"$`, w.pageWithHTML)
	sc.Step(`^another page at URL "([^"]*)" with HTML "([^"]*)"$`, w.anotherPageWithHTML)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, w.responseHeader)
	sc.Step(`^the response header "([^"]*)" is '([^']*)'$`, w.responseHeader)
	sc.Step(`^the parse config override "([^"]*)"$`, w.parseOverrideStep)
	sc.Step(`^the page is re-parsed$`, w.reparse)

	sc.Step(`^the page has (\d+) titles? and title 1 is "([^"]*)"$`, w.checkTitles)
	sc.Step(`^the page has (\d+) meta descriptions? and description 1 is "([^"]*)"$`, w.checkDescriptions)
	sc.Step(`^the page has (\d+) meta keywords$`, w.checkKeywords)
	sc.Step(`^(\d+) titles? (?:is|are) outside the head$`, w.checkTitlesOutsideHead)
	sc.Step(`^the head validity check reports invalid elements in head$`, w.checkInvalidHeadElements)
	sc.Step(`^the head validity check reports a missing head$`, w.checkMissingHead)
	sc.Step(`^the head validity check reports multiple body tags$`, w.checkMultipleBody)
	sc.Step(`^the page has (\d+) h1s? and h1 1 is "([^"]*)"$`, w.checkH1s)
	sc.Step(`^the page has (\d+) h2s$`, w.checkH2s)
	sc.Step(`^the first heading level is (\d+)$`, w.checkFirstHeadingLevel)
	sc.Step(`^meta robots 1 is "([^"]*)"$`, w.checkMetaRobots)
	sc.Step(`^the x-robots-tag directives include "([^"]*)"$`, w.checkXRobots)
	sc.Step(`^the HTML canonical is "([^"]*)"$`, w.checkHTMLCanonical)
	sc.Step(`^the HTTP canonical is "([^"]*)"$`, w.checkHTTPCanonical)
	sc.Step(`^the page has (\d+) hreflang entries$`, w.checkHreflangCount)
	sc.Step(`^hreflang "([^"]*)" points to "([^"]*)"$`, w.checkHreflangEntry)
	sc.Step(`^rel next is "([^"]*)"$`, w.checkRelNext)
	sc.Step(`^rel prev is "([^"]*)"$`, w.checkRelPrev)
	sc.Step(`^the meta refresh target is "([^"]*)"$`, w.checkMetaRefresh)
	sc.Step(`^the word count is (\d+)$`, w.checkWordCount)
	sc.Step(`^both pages have the same hash$`, w.checkSameHash)
	sc.Step(`^the page language is "([^"]*)"$`, w.checkLang)

	sc.Step(`^a hyperlink to "([^"]*)" exists$`, w.checkHyperlinkExists)
	sc.Step(`^a hyperlink to "([^"]*)" exists with anchor "([^"]*)"$`, w.checkHyperlinkAnchor)
	sc.Step(`^a hyperlink to "([^"]*)" exists with alt "([^"]*)"$`, w.checkHyperlinkAlt)
	sc.Step(`^the link to "([^"]*)" is nofollow$`, w.checkLinkNofollow)
	sc.Step(`^the link to "([^"]*)" is followed$`, w.checkLinkFollowed)
	sc.Step(`^a link of type "([^"]*)" to "([^"]*)" exists$`, w.checkTypedLink)
	sc.Step(`^the link to "([^"]*)" has position "([^"]*)"$`, w.checkLinkPosition)
	sc.Step(`^the link to "([^"]*)" has path type "([^"]*)"$`, w.checkLinkPathType)
	sc.Step(`^the link to "([^"]*)" has target "([^"]*)"$`, w.checkLinkTarget)
	sc.Step(`^the link to "([^"]*)" has rel "([^"]*)"$`, w.checkLinkRel)
	sc.Step(`^the page has (\d+) links? of type "([^"]*)"$`, w.checkLinkTypeCount)
}

func (w *world) registerIndexabilitySteps(sc *godog.ScenarioContext) {
	sc.Step(`^a URL "([^"]*)" with status (\d+)$`, w.idxURLStatus)
	sc.Step(`^a URL "([^"]*)" with a fetch error$`, w.idxURLFetchError)
	sc.Step(`^the URL is blocked by robots\.txt$`, w.idxBlocked)
	sc.Step(`^a meta refresh target "([^"]*)"$`, w.idxMetaRefresh)
	sc.Step(`^meta robots contains "([^"]*)"$`, w.idxMetaRobots)
	sc.Step(`^x-robots-tag contains "([^"]*)"$`, w.idxXRobots)
	sc.Step(`^a canonical pointing to "([^"]*)"$`, w.idxCanonical)
	sc.Step(`^the indexability config override "([^"]*)"$`, w.idxOverrideStep)
	sc.Step(`^indexability is evaluated$`, w.idxEvaluate)
	sc.Step(`^the URL is indexable$`, w.idxIsIndexable)
	sc.Step(`^the URL is non-indexable with reason "([^"]*)"$`, w.idxIsNonIndexable)
}

// --- parse step implementations ---

func (w *world) parseCfg() (*config.Config, error) {
	cfg := config.Default()
	for _, o := range w.parseOverride {
		if err := cfg.Set(o); err != nil {
			return nil, err
		}
	}
	return cfg, cfg.Validate()
}

func (w *world) doParse() error {
	cfg, err := w.parseCfg()
	if err != nil {
		return err
	}
	w.facts = parse.Parse(w.pageURL, []byte(w.pageHTML), w.respHeader, cfg)
	return nil
}

func (w *world) pageWithHTMLDoc(url string, doc *godog.DocString) error {
	w.pageURL, w.pageHTML = url, doc.Content
	return w.doParse()
}

func (w *world) pageWithHTML(url, html string) error {
	w.pageURL, w.pageHTML = url, html
	return w.doParse()
}

func (w *world) anotherPageWithHTML(url, html string) error {
	cfg, err := w.parseCfg()
	if err != nil {
		return err
	}
	w.facts2 = parse.Parse(url, []byte(html), w.respHeader, cfg)
	return nil
}

func (w *world) responseHeader(name, value string) error {
	if w.respHeader == nil {
		w.respHeader = http.Header{}
	}
	w.respHeader.Add(name, value)
	return nil
}

func (w *world) parseOverrideStep(o string) error {
	w.parseOverride = append(w.parseOverride, o)
	return nil
}

func (w *world) reparse() error { return w.doParse() }

func checkCountAndFirst(kind string, items []string, count int, first string) error {
	if len(items) != count {
		return fmt.Errorf("%s count = %d (%v), want %d", kind, len(items), items, count)
	}
	if count > 0 && items[0] != first {
		return fmt.Errorf("%s 1 = %q, want %q", kind, items[0], first)
	}
	return nil
}

func (w *world) checkTitles(count int, first string) error {
	return checkCountAndFirst("title", w.facts.Titles, count, first)
}

func (w *world) checkDescriptions(count int, first string) error {
	return checkCountAndFirst("description", w.facts.Descriptions, count, first)
}

func (w *world) checkKeywords(count int) error {
	if len(w.facts.Keywords) != count {
		return fmt.Errorf("keywords = %v, want %d", w.facts.Keywords, count)
	}
	return nil
}

func (w *world) checkTitlesOutsideHead(count int) error {
	if w.facts.TitlesOutsideHead != count {
		return fmt.Errorf("titles outside head = %d, want %d", w.facts.TitlesOutsideHead, count)
	}
	return nil
}

func (w *world) checkInvalidHeadElements() error {
	if len(w.facts.Head.InvalidElementsInHead) == 0 {
		return fmt.Errorf("no invalid head elements reported: %+v", w.facts.Head)
	}
	return nil
}

func (w *world) checkMissingHead() error {
	if !w.facts.Head.MissingHead {
		return fmt.Errorf("missing head not reported: %+v", w.facts.Head)
	}
	return nil
}

func (w *world) checkMultipleBody() error {
	if !w.facts.Head.MultipleBody {
		return fmt.Errorf("multiple body not reported: %+v", w.facts.Head)
	}
	return nil
}

func (w *world) checkH1s(count int, first string) error {
	return checkCountAndFirst("h1", w.facts.H1s, count, first)
}

func (w *world) checkH2s(count int) error {
	if len(w.facts.H2s) != count {
		return fmt.Errorf("h2s = %v, want %d", w.facts.H2s, count)
	}
	return nil
}

func (w *world) checkFirstHeadingLevel(level int) error {
	if len(w.facts.HeadingLevels) == 0 || w.facts.HeadingLevels[0] != level {
		return fmt.Errorf("heading levels = %v, want first %d", w.facts.HeadingLevels, level)
	}
	return nil
}

func (w *world) checkMetaRobots(want string) error {
	return checkCountAndFirst("meta robots", w.facts.MetaRobots, len(w.facts.MetaRobots), want)
}

func (w *world) checkXRobots(want string) error {
	for _, v := range w.facts.XRobotsTag {
		if strings.Contains(v, want) {
			return nil
		}
	}
	return fmt.Errorf("x-robots = %v, want to include %q", w.facts.XRobotsTag, want)
}

func (w *world) checkHTMLCanonical(want string) error {
	return checkCountAndFirst("html canonical", w.facts.CanonicalHTML, 1, want)
}

func (w *world) checkHTTPCanonical(want string) error {
	return checkCountAndFirst("http canonical", w.facts.CanonicalHTTP, 1, want)
}

func (w *world) checkHreflangCount(count int) error {
	if len(w.facts.HreflangHTML) != count {
		return fmt.Errorf("hreflang = %v, want %d entries", w.facts.HreflangHTML, count)
	}
	return nil
}

func (w *world) checkHreflangEntry(lang, url string) error {
	for _, h := range w.facts.HreflangHTML {
		if h.Lang == lang && h.URL == url {
			return nil
		}
	}
	return fmt.Errorf("hreflang %s -> %s not found in %v", lang, url, w.facts.HreflangHTML)
}

func (w *world) checkRelNext(want string) error {
	return checkCountAndFirst("rel next", w.facts.NextHTML, 1, want)
}

func (w *world) checkRelPrev(want string) error {
	return checkCountAndFirst("rel prev", w.facts.PrevHTML, 1, want)
}

func (w *world) checkMetaRefresh(want string) error {
	if w.facts.MetaRefreshURL != want {
		return fmt.Errorf("meta refresh = %q, want %q", w.facts.MetaRefreshURL, want)
	}
	return nil
}

func (w *world) checkWordCount(want int) error {
	if w.facts.WordCount != want {
		return fmt.Errorf("word count = %d (%q), want %d", w.facts.WordCount, w.facts.ContentText, want)
	}
	return nil
}

func (w *world) checkSameHash() error {
	if w.facts == nil || w.facts2 == nil || w.facts.Hash != w.facts2.Hash {
		return fmt.Errorf("hashes differ")
	}
	return nil
}

func (w *world) checkLang(want string) error {
	if w.facts.Lang != want {
		return fmt.Errorf("lang = %q, want %q", w.facts.Lang, want)
	}
	return nil
}

// --- link step implementations ---

func (w *world) findLink(url string) *parse.Link {
	for i := range w.facts.Links {
		if w.facts.Links[i].URL == url {
			return &w.facts.Links[i]
		}
	}
	return nil
}

func (w *world) mustFindLink(url string) (*parse.Link, error) {
	l := w.findLink(url)
	if l == nil {
		return nil, fmt.Errorf("no link to %q in %+v", url, w.facts.Links)
	}
	return l, nil
}

func (w *world) checkHyperlinkExists(url string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Type != parse.Hyperlink {
		return fmt.Errorf("link to %q has type %s, want hyperlink", url, l.Type)
	}
	return nil
}

func (w *world) checkHyperlinkAnchor(url, anchor string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Anchor != anchor {
		return fmt.Errorf("anchor = %q, want %q", l.Anchor, anchor)
	}
	return nil
}

func (w *world) checkHyperlinkAlt(url, alt string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Alt != alt {
		return fmt.Errorf("alt = %q, want %q", l.Alt, alt)
	}
	return nil
}

func (w *world) checkLinkNofollow(url string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if !l.Nofollow {
		return fmt.Errorf("link to %q is followed, want nofollow", url)
	}
	return nil
}

func (w *world) checkLinkFollowed(url string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Nofollow {
		return fmt.Errorf("link to %q is nofollow, want followed", url)
	}
	return nil
}

func (w *world) checkTypedLink(typ, url string) error {
	for _, l := range w.facts.Links {
		if string(l.Type) == typ && l.URL == url {
			return nil
		}
	}
	return fmt.Errorf("no %s link to %q in %+v", typ, url, w.facts.Links)
}

func (w *world) checkLinkPosition(url, want string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Position != want {
		return fmt.Errorf("position = %q (path %s), want %q", l.Position, l.ElemPath, want)
	}
	return nil
}

func (w *world) checkLinkPathType(url, want string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.PathType != want {
		return fmt.Errorf("path type = %q, want %q", l.PathType, want)
	}
	return nil
}

func (w *world) checkLinkTarget(url, want string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Target != want {
		return fmt.Errorf("target = %q, want %q", l.Target, want)
	}
	return nil
}

func (w *world) checkLinkRel(url, want string) error {
	l, err := w.mustFindLink(url)
	if err != nil {
		return err
	}
	if l.Rel != want {
		return fmt.Errorf("rel = %q, want %q", l.Rel, want)
	}
	return nil
}

func (w *world) checkLinkTypeCount(count int, typ string) error {
	got := 0
	for _, l := range w.facts.Links {
		if string(l.Type) == typ {
			got++
		}
	}
	if got != count {
		return fmt.Errorf("%d links of type %s, want %d (links: %+v)", got, typ, count, w.facts.Links)
	}
	return nil
}

// --- indexability step implementations ---

func (w *world) idxURLStatus(url string, status int) error {
	w.idxInput = indexability.Input{PageURL: url, StatusCode: status}
	return nil
}

func (w *world) idxURLFetchError(url string) error {
	w.idxInput = indexability.Input{PageURL: url, FetchError: "connection refused"}
	return nil
}

func (w *world) idxBlocked() error {
	w.idxInput.RobotsBlocked = true
	return nil
}

func (w *world) idxMetaRefresh(target string) error {
	w.idxInput.MetaRefreshURL = target
	return nil
}

func (w *world) idxMetaRobots(value string) error {
	w.idxInput.MetaRobots = append(w.idxInput.MetaRobots, value)
	return nil
}

func (w *world) idxXRobots(value string) error {
	w.idxInput.XRobotsTag = append(w.idxInput.XRobotsTag, value)
	return nil
}

func (w *world) idxCanonical(url string) error {
	w.idxInput.Canonicals = append(w.idxInput.Canonicals, url)
	return nil
}

func (w *world) idxOverrideStep(o string) error {
	w.idxOverride = append(w.idxOverride, o)
	return nil
}

func (w *world) idxEvaluate() error {
	cfg := config.Default()
	for _, o := range w.idxOverride {
		if err := cfg.Set(o); err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	w.idxInput.RobotsUserAgent = cfg.HTTP.RobotsUserAgent
	w.idxInput.RespectSelfRefMetaRefresh = cfg.Advanced.RespectSelfReferencingMetaRefresh
	w.idxResult = indexability.Evaluate(w.idxInput)
	return nil
}

func (w *world) idxIsIndexable() error {
	if !w.idxResult.Indexable {
		return fmt.Errorf("non-indexable with reason %q, want indexable", w.idxResult.Status)
	}
	return nil
}

func (w *world) idxIsNonIndexable(reason string) error {
	if w.idxResult.Indexable {
		return fmt.Errorf("indexable, want non-indexable with %q", reason)
	}
	if w.idxResult.Status != reason {
		return fmt.Errorf("reason = %q, want %q", w.idxResult.Status, reason)
	}
	return nil
}
