Feature: Read-only serve API
  `acrawler serve` exposes stored crawls over a localhost JSON API so other
  tools (and the desktop UI) can read crawl data without opening SQLite
  themselves. These scenarios exercise the HTTP handler against a real
  stored crawl: registry listing, dataset exports, evaluated issues, and
  JSON errors for unknown names.

  Background:
    Given a site page "/" with body "<html><head><title>Serve fixture home</title></head><body><h1>h</h1><a href='/two'>two</a> <a href='/missing'>broken</a></body></html>"
    And a site page "/two" with body "<html><head><title>Serve fixture second page</title></head><body><h1>t</h1></body></html>"
    When I crawl the site into a store
    And issues are evaluated

  Scenario: The crawl registry lists the stored crawl
    Then serving the stored crawl, GET "/api/crawls" responds 200 and contains "<crawlid>"

  Scenario: The datasets list contains the internal tab
    Then serving the stored crawl, GET "/api/crawls/<crawlid>/datasets" responds 200 and contains "internal"

  Scenario: The internal dataset contains the seed URL
    Then serving the stored crawl, GET "/api/crawls/<crawlid>/datasets/internal" responds 200 and contains "<serverurl>/"

  Scenario: The issues endpoint serves evaluated issues
    Then serving the stored crawl, GET "/api/crawls/<crawlid>/issues" responds 200 and contains "internal_client_error"

  Scenario: An unknown dataset is a JSON 404
    Then serving the stored crawl, GET "/api/crawls/<crawlid>/datasets/bogus" responds 404 and contains "error"
