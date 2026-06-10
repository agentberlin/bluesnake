Feature: Storage, pause and resume (partial crawling)
  Crawls are continuously committed to a per-crawl SQLite database (WAL):
  every processed page and every frontier mutation is durable the moment it
  happens. An interrupted crawl resumes from the stored frontier without
  re-fetching anything. The config is frozen into the crawl at start.

  Scenario: Crawls get IDs and are listed with project and status
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --project myproj --quiet"
    Then the exit code is 0
    When I run "acrawler crawls ls --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "myproj"
    And the output contains "completed"

  Scenario: An interrupted crawl resumes from where it stopped
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When the crawl is resumed from the store
    Then all 41 pages are processed in the store
    And the stored frontier is empty
    And no fixture page was fetched twice

  Scenario: Resume with a changed config is refused without --force
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When I run "acrawler resume <crawlid> --store-dir <storedir> --set speed.max_threads=9"
    Then the exit code is 2
    And the output contains "--force"

  Scenario: Deleting a crawl removes it from the registry
    Given a site page "/" linking to ""
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler crawls rm <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    When I run "acrawler crawls ls --store-dir <storedir>"
    Then the output does not contain "<crawlid>"
