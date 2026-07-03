Feature: Standalone tools (bluesnake tools)
  The tools command group runs bluesnake's site testers against live URLs
  without a crawl (DESIGN.md §5.10): the registry-driven list, the
  robots.txt tester (local file or live fetch), and the sitemap tester.
  Tool runs are throwaway — nothing is persisted.

  Scenario: tools list enumerates the registry
    When I run "bluesnake tools list"
    Then the exit code is 0
    And the output contains "robots"
    And the output contains "sitemap"

  Scenario: robots tester fetches the live file and reports its health
    Given a robots.txt file:
      """
      User-agent: *
      Disallow: /private/
      Sitemap: /sitemap.xml
      """
    And the test server serves the background robots.txt
    When I run "bluesnake tools robots --site <serverurl> --robots-user-agent somebot <serverurl>/private/x <serverurl>/ok"
    Then the exit code is 0
    And the output contains "BLOCKED  <serverurl>/private/x"
    And the output contains "ALLOWED  <serverurl>/ok"
    And the output contains "findings: none"

  Scenario: robots tester surfaces file-level findings
    Given a robots.txt file:
      """
      User-agent: *
      Disallow: /
      Disallow /broken
      """
    And the test server serves the background robots.txt
    When I run "bluesnake tools robots --site <serverurl>"
    Then the exit code is 0
    And the output contains "robots.txt Blocks All Crawlers"
    And the output contains "robots.txt Invalid Lines"
    And the output contains "robots.txt Missing Sitemap Directive"

  Scenario: sitemap tester validates an explicit sitemap URL
    Given a site page "/sitemap.xml" with body:
      """
      <?xml version="1.0" encoding="UTF-8"?>
      <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
        <url><loc><serverurl>/</loc><lastmod>not-a-date</lastmod></url>
      </urlset>
      """
    When I run "bluesnake tools sitemap <serverurl>/sitemap.xml"
    Then the exit code is 0
    And the output contains "OK       <serverurl>/sitemap.xml"
    And the output contains "XML Sitemap Invalid lastmod Values"

  Scenario: sitemap tester reports a site without any sitemap
    Given a site page "/" linking to ""
    When I run "bluesnake tools sitemap <serverurl>"
    Then the exit code is 0
    And the output contains "no sitemap found"
    And the output contains "No XML Sitemap Found"

  Scenario: AI-bot tester reports robots verdicts and edge blocks
    Given a robots.txt file:
      """
      User-agent: GPTBot
      Disallow: /

      User-agent: *
      Allow: /
      Sitemap: /sitemap.xml
      """
    And the test server serves the background robots.txt
    And a site page "/" linking to ""
    When I run "bluesnake tools aibots <serverurl>"
    Then the exit code is 0
    And the output contains "GPTBot"
    And the output contains "BLOCKED (line 2: Disallow: /)"
    And the output contains "control token (never fetches)"
    And the output contains "AI Bot Blocked by robots.txt"
    And the output contains "User-Agent-based blocking only"

  Scenario: llms tester validates the site files
    Given a site page "/llms.txt" with body:
      """
      # Healthy Project

      > A tidy summary.

      ## Docs
      - [Guide](/guide): the guide
      """
    When I run "bluesnake tools llms <serverurl>"
    Then the exit code is 0
    And the output contains "OK       <serverurl>/llms.txt"
    And the output contains "Missing /llms-full.txt"

  Scenario: structured-data tester validates a live page
    Given a site page "/p" with body:
      """
      <html><head><script type="application/ld+json">
      {"@context":"https://schema.org","@type":"Product","name":"Widget","offers":{"@type":"Offer","price":"10","priceCurrency":"USD"}}
      </script></head><body></body></html>
      """
    When I run "bluesnake tools structured <serverurl>/p"
    Then the exit code is 0
    And the output contains "types    Product, Offer"
    And the output contains "Rich Result Validation Errors"

  Scenario: serp preview reports pixel overflow and truncation
    When I run "bluesnake tools serp --title WideWidgetWarehouseWideWidgetWarehouseWideWidgetWarehouse"
    Then the exit code is 0
    And the output contains "displays as"
    And the output contains "Over X Pixels"

  Scenario: the retired robots test command is gone
    When I run "bluesnake robots"
    Then the exit code is 1
