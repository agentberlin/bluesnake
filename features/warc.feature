Feature: WARC archiving
  With extraction.store_warc enabled, every fetched response is appended to
  a WARC/1.1 archive (archive.warc.gz, one gzip member per record) stored
  next to the crawl database, so a crawl doubles as a replayable capture.

  Scenario: Crawled responses are archived to WARC
    Given a site page "/" linking to "/a"
    And a site page "/a" with body "<p>leaf</p>"
    And the crawl config override "extraction.store_warc=true"
    When I crawl the site into a store
    Then the stored crawl archive contains a response record for "/"
    And the stored crawl archive contains a response record for "/a"
