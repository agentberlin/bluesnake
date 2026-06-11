Feature: Issue detection
  Crawled pages are evaluated against the audit catalogue; each issue has a
  stable id, severity (issue/warning/opportunity) and priority. The full
  catalogue is unit-tested; these scenarios verify end-to-end detection
  through a real crawl and the CLI.

  Scenario: A pathological site yields the expected issues
    Given a site page "/" with body:
      """
      <html><head></head><body>
        <a href="/dup1">one</a> <a href="/dup2">two</a>
        <a href="/broken">broken</a> <a href="/thin">thin</a>
        <a href="here">click here</a>
      </body></html>
      """
    And a site page "/dup1" with body "<html><head><title>Duplicate Title Page</title></head><body><h1>a</h1><p>same body</p></body></html>"
    And a site page "/dup2" with body "<html><head><title>Duplicate Title Page</title></head><body><h1>b</h1><p>same body</p></body></html>"
    And a site page "/thin" with body "<html><head><title>A thin page with a good length title</title></head><body><h1>thin</h1><h2>s</h2><p>few words only here</p></body></html>"
    And a site page "/here" with body "<html><head><title>Target of nondescriptive anchor text</title></head><body><h1>t</h1><h2>s</h2></body></html>"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "title_missing"
    And the page "/" has issue "links_non_descriptive_anchor"
    And the page "/broken" has issue "internal_client_error"
    And the page "/dup1" has issue "title_duplicate"
    And the page "/dup2" has issue "title_duplicate"
    And the page "/thin" has issue "content_low_word_count"
    And the page "/thin" does not have issue "title_missing"

  Scenario: The issues CLI prints a summary
    Given a site page "/" with body "<html><body><a href='/missing-page'>x</a></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake issues <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "internal_client_error"
    And the output contains "title_missing"

  Scenario: Listing URLs for one issue
    Given a site page "/" with body "<html><body><a href='/gone'>x</a></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake issues <crawlid> --store-dir <storedir> --urls internal_client_error"
    Then the exit code is 0
    And the output contains "/gone"
