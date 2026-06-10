Feature: Robots.txt handling
  acrawler obeys robots.txt with Google REP semantics (RFC 9309 + Google
  extensions): user-agent group selection by longest matching token with
  fallback to *, longest-path-match rule precedence, allow wins ties,
  wildcards * and $ supported. Three modes: respect, ignore (file not even
  fetched), ignore-report (fetched and reported, not obeyed).

  Background:
    Given a robots.txt file:
      """
      User-agent: *
      Disallow: /private/
      Allow: /private/public-bit
      Disallow: /*.pdf$

      User-agent: acrawler
      Disallow: /only-for-others/

      Sitemap: https://ex.com/sitemap.xml
      """

  Scenario: Group selection prefers our specific token
    Then "https://ex.com/private/x" is allowed for robots user-agent "acrawler"
    And "https://ex.com/only-for-others/x" is blocked for robots user-agent "acrawler"

  Scenario: Generic agents fall back to the star group
    Then "https://ex.com/private/x" is blocked for robots user-agent "somebot"
    And "https://ex.com/private/public-bit/y" is allowed for robots user-agent "somebot"

  Scenario: Longest match wins and allow wins ties
    Then "https://ex.com/private/public-bit" is allowed for robots user-agent "somebot"
    And "https://ex.com/private/other" is blocked for robots user-agent "somebot"

  Scenario: Wildcard with end anchor
    Then "https://ex.com/doc.pdf" is blocked for robots user-agent "somebot"
    And "https://ex.com/doc.pdfx" is allowed for robots user-agent "somebot"
    And "https://ex.com/a/deep/doc.pdf" is blocked for robots user-agent "somebot"

  Scenario: Token matching is case-insensitive and prefix-based
    Then "https://ex.com/only-for-others/x" is blocked for robots user-agent "ACrawler-Images"

  Scenario: Blocked verdicts carry the matched rule line
    Then blocking "https://ex.com/private/x" for "somebot" reports matched line 2

  Scenario: Sitemaps are discovered
    Then the robots file lists the sitemap "https://ex.com/sitemap.xml"

  Scenario: An empty or missing robots file allows everything
    Given a robots.txt file:
      """
      """
    Then "https://ex.com/anything" is allowed for robots user-agent "somebot"

  Scenario: robots tester subcommand
    Given a robots.txt file:
      """
      User-agent: *
      Disallow: /private/
      """
    When I run "acrawler robots test --robots-file <robotsfile> --robots-user-agent somebot https://ex.com/private/x https://ex.com/ok"
    Then the exit code is 0
    And the output contains "BLOCKED  https://ex.com/private/x"
    And the output contains "Disallow: /private/"
    And the output contains "ALLOWED  https://ex.com/ok"

  Scenario: Blocked URLs are reported in crawl results with the matched line
    Given the test server serves the background robots.txt
    And a site page "/" linking to "/private/x,/ok"
    And a site page "/private/x" linking to ""
    And a site page "/ok" linking to ""
    And the crawl config override "http.robots_user_agent=somebot"
    When I crawl the site
    Then the crawl page "/private/x" has crawl state "blocked_robots"
    And the crawl page "/private/x" has matched robots line 2
    And the page "/private/x" was not requested
    And the crawl page "/ok" has crawl state "crawled"

  Scenario: Ignore mode never fetches robots.txt
    Given the test server serves the background robots.txt
    And a site page "/" linking to "/private/x"
    And a site page "/private/x" linking to ""
    And the crawl config override "http.robots_user_agent=somebot"
    And the crawl config override "robots.mode=ignore"
    When I crawl the site
    Then the page "/robots.txt" was not requested
    And the crawl page "/private/x" has status code 200

  Scenario: Ignore-report mode fetches but does not obey
    Given the test server serves the background robots.txt
    And a site page "/" linking to "/private/x"
    And a site page "/private/x" linking to ""
    And the crawl config override "http.robots_user_agent=somebot"
    And the crawl config override "robots.mode=ignore-report"
    When I crawl the site
    Then the page "/robots.txt" was requested exactly 1 times
    And the crawl page "/private/x" has status code 200

  Scenario: Custom robots.txt overrides the live file
    Given the test server serves the background robots.txt
    And a site page "/" linking to "/private/x,/shop/item"
    And a site page "/private/x" linking to ""
    And a site page "/shop/item" linking to ""
    And a custom robots.txt for the test server:
      """
      User-agent: *
      Disallow: /shop/
      """
    And the crawl config override "http.robots_user_agent=somebot"
    When I crawl the site
    Then the crawl page "/private/x" has status code 200
    And the crawl page "/shop/item" has crawl state "blocked_robots"
    And the page "/robots.txt" was not requested
