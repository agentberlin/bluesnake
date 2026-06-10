Feature: Crawl limits
  Every limit from Spider > Limits: total, depth, per-depth, folder depth,
  query strings, redirects-to-follow, URL length, links per page, page size,
  per-path caps. Over-limit URLs are silently not requested.

  Scenario: Total URL limit stops the crawl
    Given a site page "/" with 30 generated links
    When the crawl config override "limits.max_urls=10" is set
    And I crawl the site
    Then at most 10 pages have crawl state "crawled"

  Scenario: Depth limit
    Given a site page "/" linking to "/d1"
    And a site page "/d1" linking to "/d2"
    And a site page "/d2" linking to "/d3"
    And a site page "/d3" linking to ""
    And the crawl config override "limits.max_depth=1"
    When I crawl the site
    Then the crawl page "/d1" has crawl state "crawled"
    And the page "/d2" was not requested

  Scenario: Folder depth limit
    Given a site page "/" linking to "/a/"
    And a site page "/a/" linking to "/a/b/"
    And a site page "/a/b/" linking to "/a/b/c/"
    And a site page "/a/b/c/" linking to ""
    And the crawl config override "limits.max_folder_depth=2"
    When I crawl the site
    Then the crawl page "/a/b/" has crawl state "crawled"
    And the page "/a/b/c/" was not requested

  Scenario: Query string limit
    Given a site page "/" linking to "/p?a=1&b=2&c=3"
    And a site page "/p" linking to ""
    And the crawl config override "limits.max_query_strings=2"
    When I crawl the site
    Then the page "/p" was not requested

  Scenario: URL length limit
    Given a site page "/" linking to a path of 120 characters
    And the crawl config override "limits.max_url_length=100"
    When I crawl the site
    Then only 1 pages were requested

  Scenario: Max links per page caps discovery
    Given a site page "/" with 50 generated links
    And the crawl config override "limits.max_links_per_page=20"
    When I crawl the site
    Then at most 21 pages were requested

  Scenario: Redirect chain limit
    Given a test server redirect chain from "/r" of length 8
    And the crawl config override "limits.max_redirects=5"
    When I crawl the site starting at "/r0"
    Then exactly 6 chain URLs under "/r" were requested

  Scenario: Per-path limit
    Given a site page "/" with 10 generated links under "/blog/" and 10 under "/shop/"
    And a path limit of 5 for "/blog/"
    When I crawl the site
    Then at most 5 pages under "/blog/" were crawled
    And 10 pages under "/shop/" were crawled

  Scenario: Oversized pages are skipped
    Given a test server route "/big" responding 200 with a body of 200 KB
    And a site page "/" linking to "/big"
    And the crawl config override "limits.max_page_size_kb=100"
    When I crawl the site
    Then the crawl page "/big" has crawl state "skipped_too_large"
