Feature: List mode
  Audit an uploaded set of URLs: depth 0 by default (only the listed URLs),
  robots.txt ignored by default, every listed host treated as internal, and
  optional redirect-chain following for migration audits.

  Scenario: A URL list file is audited at depth 0
    Given a site page "/a" linking to "/not-followed"
    And a site page "/b" linking to ""
    And a site page "/not-followed" linking to ""
    And a URL list file containing "<serverurl>/a" and "<serverurl>/b"
    When I run "acrawler list <listfile> --store-dir <storedir> --quiet"
    Then the exit code is 0
    And the page "/a" was requested exactly 1 times
    And the page "/b" was requested exactly 1 times
    And the page "/not-followed" was not requested
    And the page "/robots.txt" was not requested

  Scenario: Redirect chains are followed to the end with --follow-redirects
    Given a test server redirect chain from "/r" of length 3
    And a URL list file containing "<serverurl>/r0" and "<serverurl>/r0"
    When I run "acrawler list <listfile> --store-dir <storedir> --quiet --follow-redirects"
    Then the exit code is 0
    And the page "/end" was requested exactly 1 times

  Scenario: A sitemap can be the list source
    Given a site page "/from-sitemap" linking to ""
    And a test server route "/list.xml" responding 200 with body "<urlset><url><loc><serverurl>/from-sitemap</loc></url></urlset>"
    When I run "acrawler list --sitemap <serverurl>/list.xml --store-dir <storedir> --quiet"
    Then the exit code is 0
    And the page "/from-sitemap" was requested exactly 1 times
