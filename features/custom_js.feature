@chrome
Feature: Custom JavaScript snippets
  With rendering.mode=javascript, user-supplied JavaScript snippets run in
  the rendered page over the DevTools protocol: action snippets mutate the
  page first (results discarded), then extraction snippets evaluate and each
  value is stored per page as a custom result of kind "js". Snippets scoped
  by content_types only store results for matching pages.

  These scenarios require a local Chrome/Chromium and are excluded from the
  default run (tag @chrome); the behaviour is verified by Go tests in
  internal/render and internal/crawler that skip without Chrome.

  Scenario: An extraction snippet stores its value per page
    Given a site page "/" with body "<html><head><title>Snippet Target</title></head><body><h1>x</h1></body></html>"
    And a custom JS extraction snippet "get-title" containing "document.title"
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site
    Then the crawl page "/" has custom result "get-title" with value "Snippet Target"

  Scenario: Action snippets run before extraction snippets
    Given a site page "/" with body "<html><head><title>Raw</title></head><body><h1>x</h1></body></html>"
    And a custom JS action snippet "set-title" containing "document.title = 'changed by action'"
    And a custom JS extraction snippet "get-title" containing "document.title"
    And the crawl config override "rendering.mode=javascript"
    When I crawl the site
    Then the crawl page "/" has custom result "get-title" with value "changed by action"
