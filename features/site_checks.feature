Feature: Site-level checks during a crawl
  A full-domain audit — a root-seeded spider crawl that is not scope-narrowed
  — also audits the seed host's robots.txt and XML sitemaps out-of-band
  (DESIGN.md §5.10). The reports are stored with the crawl and their
  findings surface as ordinary issues after analysis. site_checks.enabled
  gates the pass: auto (the default heuristic) | always | never.

  Scenario: A full-domain crawl of a bare site reports the missing files
    Given a site page "/" linking to ""
    When I crawl the site into a store
    And analysis is run
    Then the page "/robots.txt" has issue "robots_txt_missing"
    And the page "/" has issue "sitemap_missing"

  Scenario: A healthy site yields no site-check findings
    Given a robots.txt file:
      """
      User-agent: *
      Disallow:
      Sitemap: /sitemap.xml
      """
    And the test server serves the background robots.txt
    And a site page "/sitemap.xml" with body:
      """
      <?xml version="1.0" encoding="UTF-8"?>
      <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
        <url><loc><serverurl>/</loc><lastmod>2026-01-15</lastmod></url>
      </urlset>
      """
    And a site page "/" linking to ""
    When I crawl the site into a store
    And analysis is run
    Then the page "/robots.txt" does not have issue "robots_txt_missing"
    And the page "/robots.txt" does not have issue "robots_txt_no_sitemap"
    And the page "/sitemap.xml" does not have issue "sitemap_invalid_xml"
    And the page "/" does not have issue "sitemap_missing"

  Scenario: An unhealthy robots.txt and sitemap surface as issues
    Given a robots.txt file:
      """
      User-agent: *
      Disallow: /
      Disallow /broken
      Sitemap: /sitemap.xml
      """
    And the test server serves the background robots.txt
    And a site page "/sitemap.xml" with body:
      """
      not xml at all <
      """
    And a site page "/" linking to ""
    When I crawl the site into a store
    And analysis is run
    Then the page "/robots.txt" has issue "robots_txt_blocks_all"
    And the page "/robots.txt" has issue "robots_txt_invalid_lines"
    And the page "/sitemap.xml" has issue "sitemap_invalid_xml"

  Scenario: A robots.txt disallowing an AI bot surfaces as an issue
    Given a robots.txt file:
      """
      User-agent: GPTBot
      Disallow: /

      User-agent: *
      Allow: /
      Sitemap: /sitemap.xml
      """
    And the test server serves the background robots.txt
    And a site page "/sitemap.xml" with body:
      """
      <?xml version="1.0" encoding="UTF-8"?>
      <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
        <url><loc><serverurl>/</loc></url>
      </urlset>
      """
    And a site page "/" linking to ""
    When I crawl the site into a store
    And analysis is run
    Then the page "/" has issue "ai_bot_blocked_robots"
    And the page "/" does not have issue "ai_bots_all_blocked_robots"
    And the page "/" does not have issue "ai_bot_blocked_live"

  Scenario: A path-seeded crawl skips the site checks
    Given a site page "/blog/" linking to ""
    And the crawl config override "sitemaps.crawl_linked=false"
    When I crawl the site starting at "/blog/" into a store
    And analysis is run
    Then the page "/robots.txt" does not have issue "robots_txt_missing"
    And the page "/blog/" does not have issue "sitemap_missing"

  Scenario: site_checks.enabled=never disables the pass on a full-domain crawl
    Given a site page "/" linking to ""
    And the crawl config override "site_checks.enabled=never"
    When I crawl the site into a store
    And analysis is run
    Then the page "/robots.txt" does not have issue "robots_txt_missing"
    And the page "/" does not have issue "sitemap_missing"

  Scenario: site_checks.enabled=always forces the pass on a path crawl
    Given a site page "/blog/" linking to ""
    And the crawl config override "site_checks.enabled=always"
    And the crawl config override "sitemaps.crawl_linked=false"
    When I crawl the site starting at "/blog/" into a store
    And analysis is run
    Then the page "/robots.txt" has issue "robots_txt_missing"
