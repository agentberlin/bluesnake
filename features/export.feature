Feature: Exports, reports and sitemap generation
  Every tab, the link graph, and all issues export as CSV/JSON/JSONL/XLSX.
  Datasets are discoverable; any tab can be filtered to one issue's URLs.
  Named reports cover the crawl overview, chains, insecure content and
  orphans. XML sitemaps generate from the crawl with auto-splitting.

  Background:
    Given a site page "/" linking to "/a,/gone"
    And a site page "/a" with body "<html><head><title>Page A has a nice title</title></head><body><h1>A</h1><h2>s</h2></body></html>"

  Scenario: Exports are discoverable
    When I run "acrawler export --list"
    Then the exit code is 0
    And the output contains "internal"
    And the output contains "issues"

  Scenario: Tab export as CSV
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler export <crawlid> internal --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "url,status_code"
    And the output contains "Page A has a nice title"

  Scenario: Issue-filtered export
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler export <crawlid> response_codes --store-dir <storedir> --filter internal_client_error"
    Then the exit code is 0
    And the output contains "/gone"
    And the output does not contain literal "/a,"

  Scenario: Issues export as JSONL
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler export <crawlid> issues --store-dir <storedir> --format jsonl"
    Then the exit code is 0
    And the output contains "internal_client_error"
    And the output contains "severity"

  Scenario: Crawl overview report
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler report <crawlid> crawl_overview --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "total"
    And the output contains "status:4xx"

  Scenario: Sitemap generation
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler sitemap <crawlid> --store-dir <storedir> -o <storedir>/maps"
    Then the exit code is 0
    And the file "maps/sitemap.xml" in the store dir contains "<serverurl>/a"
    And the file "maps/sitemap.xml" in the store dir does not contain "/gone"
