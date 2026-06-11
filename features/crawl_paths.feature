Feature: Crawl path report
  Every stored page reports how it was discovered: the chain of first
  discovering pages from the seed to the URL, joined with " -> ", plus
  the number of hops in that chain.

  Background:
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to "/b"
    And a site page "/b" with body "<html><body><p>deepest</p></body></html>"

  Scenario: crawl_paths is a listed report
    When I run "acrawler report --list"
    Then the exit code is 0
    And the output contains "crawl_paths"

  Scenario: Crawl paths report as CSV
    When I run "acrawler crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "acrawler report <crawlid> crawl_paths --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "url,hops,path"
    And the output contains "/a -> "
    And the output contains "/b,2,"
