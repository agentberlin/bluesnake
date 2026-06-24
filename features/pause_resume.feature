Feature: Storage, pause and resume (partial crawling)
  Crawls are continuously committed to a per-crawl SQLite database (WAL):
  every processed page and every frontier mutation is durable the moment it
  happens. An interrupted crawl resumes from the stored frontier without
  re-fetching anything. The config is frozen into the crawl at start.

  Scenario: Crawls get IDs and are listed with status
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    Then the exit code is 0
    When I run "bluesnake crawls ls --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "completed"

  Scenario: An interrupted crawl resumes from where it stopped
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When the crawl is resumed from the store
    Then all 41 pages are processed in the store
    And the stored frontier is empty
    And no fixture page was fetched twice

  Scenario: Resuming to completion finalises issues and analysis like a fresh crawl
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When I run "bluesnake resume <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the stored crawl has issues recorded

  Scenario: A resumed crawl reports the same final URL counts as a straight crawl
    # The 41-page fixture has no blocked or errored URLs, so a straight crawl
    # reports 41 crawled / 41 total. Resuming to completion must report the same
    # cumulative counts in the registry — over the full stored graph — not just
    # the resumed session's slice (the bug: counts came from the per-session run).
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When I run "bluesnake resume <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the registry reports 41 crawled and 41 total for the resumed crawl

  Scenario: Resuming recomputes crawl depth over the full two-session graph
    # /deep is crawled in the first session at admit-time depth 3 (via /a -> /b);
    # the shorter path / -> /shortcut -> /deep (depth 2) is only completed on
    # resume. The resumed crawl must recompute depths over both sessions so
    # /deep ends at 2, matching a fresh crawl (Screaming Frog parity), instead
    # of keeping the stale depth 3.
    Given a stored cross-linked crawl interrupted before the shortcut page
    When I run "bluesnake resume <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the stored crawl page "/deep" has depth 2
    And the stored crawl page "/shortcut" has depth 1

  Scenario: List-mode resume keeps uploaded seeds at depth 0
    # Every uploaded URL is a depth-0 seed. The full seed set is persisted, so
    # resume re-roots the depth recompute from every seed and each stays at
    # depth 0 — instead of rerooting from a single seed and NULLing the others.
    Given a stored list-mode crawl with one seed pending
    When I run "bluesnake resume <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the stored crawl page "/" has depth 0
    And the stored crawl page "/b" has depth 0

  Scenario: Resume with a changed config is refused without --force
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When I run "bluesnake resume <crawlid> --store-dir <storedir> --set speed.max_threads=9"
    Then the exit code is 2
    And the output contains "--force"

  Scenario: Deleting a crawl removes it from the registry
    Given a site page "/" linking to ""
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake crawls rm <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    When I run "bluesnake crawls ls --store-dir <storedir>"
    Then the output does not contain "<crawlid>"
