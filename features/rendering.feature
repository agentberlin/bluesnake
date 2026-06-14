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

  Scenario: A JS-injected hyperlink is reported as a JavaScript link
    Given a site page "/" with a script that injects a link to "/js-target"
    And a site page "/js-target" linking to ""
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "js_contains_links"

  Scenario: A JS-injected image is not counted as a JavaScript link
    Given a site page "/" with a script that injects an image "/pic.png"
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "js_contains_links"

  Scenario: XHR/fetch endpoints observed during rendering are not crawled as pages
    Given a site page "/" with a script that fetches "/api/data"
    And a test server route "/api/data" responding 200 with body "{}"
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site
    # The browser issues the fetch, but the crawler buckets XHR under JavaScript
    # resources (crawl off by default) and never enqueues it as a page.
    Then the crawl has no page record for "/api/data"

  Scenario: XHR endpoints become pages when JavaScript resources are crawled
    Given a site page "/" with a script that fetches "/api/data"
    And a test server route "/api/data" responding 200 with body "{}"
    And the crawl config override "rendering.mode=javascript"
    And the crawl config override "resources.javascript.crawl=true"
    When I crawl the site
    Then the crawl page "/api/data" has crawl state "crawled"
