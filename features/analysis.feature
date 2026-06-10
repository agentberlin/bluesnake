Feature: Post-crawl analysis
  After a crawl, the analysis pass computes whole-graph results: link scores,
  redirect/canonical chains and loops, near-duplicate content, hreflang and
  pagination reciprocity, and sitemap set operations. It is re-runnable
  without recrawling.

  Scenario: Redirect chains are detected through a real crawl
    Given a test server route "/a" redirecting 301 to "/b"
    And a test server route "/b" redirecting 301 to "/c"
    And a site page "/c" linking to ""
    And a site page "/" linking to "/a"
    When I crawl the site into a store
    And analysis is run
    Then the page "/a" has issue "redirect_chain"
    And the page "/b" does not have issue "redirect_chain"

  Scenario: Sitemap orphans are found
    Given a site page "/" linking to "/linked"
    And a site page "/linked" linking to ""
    And a site page "/orphan" linking to ""
    And a test server route "/sitemap.xml" responding 200 with body "<urlset><url><loc><serverurl>/linked</loc></url><url><loc><serverurl>/orphan</loc></url></urlset>"
    And the crawl config override "sitemaps.crawl_linked=true"
    And the crawl config override "sitemaps.urls=['<serverurl>/sitemap.xml']"
    When I crawl the site into a store
    And analysis is run
    Then the page "/orphan" has issue "sitemap_orphan"
    And the page "/linked" does not have issue "sitemap_orphan"
    And the page "/" has issue "sitemap_not_in_sitemap"

  Scenario: The analyze CLI re-runs analysis on a stored crawl
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler analyze <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "Analysis:"
