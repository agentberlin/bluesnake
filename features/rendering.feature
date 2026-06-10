@chrome
Feature: JavaScript rendering
  With rendering.mode=javascript, pages render in headless Chrome; the raw
  and rendered DOMs are parsed separately and diffed (titles/descriptions/
  H1/canonical updated by JS, rendered-only links with origin=rendered,
  word-count change, console errors), and JS-injected links join discovery.

  These scenarios require a local Chrome/Chromium and are excluded from the
  default run (tag @chrome); the behaviour is verified by Go tests in
  internal/render and internal/crawler that skip without Chrome.

  Scenario: JS-injected links are discovered
    Given a site page "/" with a script that injects a link to "/js-only"
    And a site page "/js-only" linking to ""
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site
    Then the crawl page "/js-only" has crawl state "crawled"
