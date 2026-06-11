Feature: Spider mode crawl
  Recursive crawl from a start URL: same-authority URLs are internal and
  followed; externals get a status check only; redirects are followed as new
  discoveries; nofollow and start-folder scoping are honoured.

  Scenario: A small site is fully crawled with correct depths
    Given a site page "/" linking to "/a,/b"
    And a site page "/a" linking to "/b,/c"
    And a site page "/b" linking to "/"
    And a site page "/c" linking to ""
    When I crawl the site
    Then 4 pages have crawl state "crawled"
    And the crawl page "/" has depth 0
    And the crawl page "/a" has depth 1
    And the crawl page "/c" has depth 2
    And the page "/b" was requested exactly 1 times

  Scenario: External links are status-checked but not followed
    Given a second test server page "/page" linking onward to "/onward"
    And a site page "/" linking to "<external>/page"
    And the crawl config override "links.external.store=true"
    And the crawl config override "links.external.crawl=true"
    When I crawl the site
    Then the external page "/page" has status code 200
    And the external page "/page" is not parsed
    And the second server page "/onward" was not requested

  Scenario: Redirects are data, and targets are discovered
    Given a test server route "/old" redirecting 301 to "/new"
    And a site page "/" linking to "/old"
    And a site page "/new" linking to ""
    When I crawl the site
    Then the crawl page "/old" has status code 301
    And the crawl page "/old" has redirect type "http"
    And the crawl page "/old" is non-indexable in the crawl
    And the crawl page "/new" has crawl state "crawled"

  Scenario: Meta refresh targets are discovered
    Given a site page "/m" with body:
      """
      <html><head><meta http-equiv="refresh" content="0;url=/target"></head><body></body></html>
      """
    And a site page "/" linking to "/m"
    And a site page "/target" linking to ""
    When I crawl the site
    Then the crawl page "/target" has crawl state "crawled"
    And the crawl page "/m" has redirect type "meta_refresh"

  Scenario: nofollow links are not followed by default
    Given a site page "/" with body:
      """
      <html><body><a href="/hidden" rel="nofollow">x</a></body></html>
      """
    And a site page "/hidden" linking to ""
    When I crawl the site
    Then the page "/hidden" was not requested

  Scenario: follow_internal_nofollow enables following
    Given a site page "/" with body:
      """
      <html><body><a href="/hidden" rel="nofollow">x</a></body></html>
      """
    And a site page "/hidden" linking to ""
    And the crawl config override "scope.follow_internal_nofollow=true"
    When I crawl the site
    Then the crawl page "/hidden" has crawl state "crawled"

  Scenario: Excluded URLs are never requested
    Given a site page "/" linking to "/keep,/skip/this"
    And a site page "/keep" linking to ""
    And a site page "/skip/this" linking to ""
    And the crawl config override "scope.exclude=['/skip/']"
    When I crawl the site
    Then the page "/skip/this" was not requested
    And the crawl page "/keep" has crawl state "crawled"

  Scenario: Outside-start-folder pages are checked but not followed by default
    Given a site page "/blog/" linking to "/blog/post,/about"
    And a site page "/blog/post" linking to ""
    And a site page "/about" linking to "/other"
    And a site page "/other" linking to ""
    When I crawl the site starting at "/blog/"
    Then the crawl page "/about" has crawl state "crawled"
    And the page "/other" was not requested

  Scenario: crawl_outside_start_folder lifts the folder restriction
    Given a site page "/blog/" linking to "/about"
    And a site page "/about" linking to "/other"
    And a site page "/other" linking to ""
    And the crawl config override "scope.crawl_outside_start_folder=true"
    When I crawl the site starting at "/blog/"
    Then the crawl page "/other" has crawl state "crawled"

  Scenario: Disabling check_links_outside_start_folder confines the crawl entirely
    Given a site page "/blog/" linking to "/about"
    And a site page "/about" linking to ""
    And the crawl config override "scope.check_links_outside_start_folder=false"
    When I crawl the site starting at "/blog/"
    Then the page "/about" was not requested

  Scenario: The crawl CLI prints a summary
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "acrawler crawl <serverurl>/"
    Then the exit code is 0
    And the output contains "Crawled 2 URLs"
