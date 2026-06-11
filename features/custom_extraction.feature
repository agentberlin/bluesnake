Feature: Custom search and custom extraction
  Config-driven searches (contains / does-not-contain, plain or regex, over
  HTML, page text, or an element scope) and extractors (XPath, CSS selector,
  regex) run against every internal 2xx HTML page; results are stored and
  exportable.

  Scenario: Searches and extractors run during a crawl
    Given a site page "/" with body "<html><head><title>Shop here for things</title></head><body><h1>shop</h1><h2>s</h2><div class='price'>$10</div><div class='price'>$20</div></body></html>"
    And the crawl config override "custom_search=[{name: prices-mentioned, mode: contains, pattern: price}]"
    And the crawl config override "custom_extraction=[{name: prices, type: css, expression: div.price}]"
    When I crawl the site into a store
    Then the crawl page "/" has custom result "prices-mentioned" with value "2"
    And the crawl page "/" has custom result "prices" with value "$10 | $20"

  Scenario: Custom results export through the CLI
    Given a site page "/" with body "<html><head><title>Shop here for things</title></head><body><h1>s</h1><h2>x</h2><span id='sku'>AB-12</span></body></html>"
    And a config file with contents:
      """
      custom_extraction:
        - {name: sku, type: css, expression: "#sku"}
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --config <configfile> --quiet"
    And I run "bluesnake export <crawlid> custom --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "AB-12"
