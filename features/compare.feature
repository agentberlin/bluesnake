Feature: Crawl comparison
  Two stored crawls diff into per-issue deltas using Screaming Frog's four
  buckets (Added/New/Removed/Missing) plus element-level change detection.

  Scenario: Comparing two crawls of a changed site
    Given a site page "/" linking to "/stable,/fixed"
    And a site page "/stable" with body "<html><head><title>A stable page title here ok</title></head><body><h1>s</h1><h2>x</h2></body></html>"
    And a site page "/fixed" with body "<html><body><h1>no title yet</h1></body></html>"
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And the site page "/fixed" changes to body "<html><head><title>Now this page has a title</title></head><body><h1>f</h1><h2>x</h2></body></html>"
    And I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler compare <firstcrawlid> <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "title_missing"
    And the output contains "removed"
    And the output contains "Pages: 3 -> 3"
