Feature: HTTP protocol version capture
  The protocol version negotiated for each response (HTTP/1.1, HTTP/2.0) is
  recorded per page, persisted in the crawl store, and exported in the
  internal and external tabs as an http_version column after content_type.

  Scenario: Exported internal tab carries the HTTP version
    Given a site page "/" with body "<html><head><title>Home page title here</title></head><body><h1>Home</h1></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake export <crawlid> internal --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "http_version"
    And the output contains "HTTP/1.1"
