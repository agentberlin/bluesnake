Feature: On-page element extraction
  Every HTML response is parsed into PageFacts: titles, meta descriptions,
  keywords, headings, directives, canonicals, pagination, hreflang, meta
  refresh, base href, content metrics and head validity. All instances are
  counted; "outside <head>" placement is detected the way Google sees it
  (an invalid element ends the head).

  Scenario: Titles and descriptions are extracted with counts
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><head>
        <title>First title</title>
        <title>Second title</title>
        <meta name="description" content="The description">
        <meta name="keywords" content="a, b">
      </head><body></body></html>
      """
    Then the page has 2 titles and title 1 is "First title"
    And the page has 1 meta description and description 1 is "The description"
    And the page has 1 meta keywords

  Scenario: Elements after an invalid head element count as outside the head
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><head>
        <div>oops</div>
        <title>Late title</title>
      </head><body></body></html>
      """
    Then 1 title is outside the head
    And the head validity check reports invalid elements in head

  Scenario: Headings and their document order
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><body>
        <h2>Jumped</h2>
        <h1>Main</h1>
        <h2>Sub</h2>
      </body></html>
      """
    Then the page has 1 h1 and h1 1 is "Main"
    And the page has 2 h2s
    And the first heading level is 2

  Scenario: Directives from meta robots and X-Robots-Tag header
    Given the response header "X-Robots-Tag" is "noarchive"
    And a page at URL "https://ex.com/p" with HTML:
      """
      <html><head><meta name="robots" content="noindex, nofollow"></head><body></body></html>
      """
    Then meta robots 1 is "noindex, nofollow"
    And the x-robots-tag directives include "noarchive"

  Scenario: Canonicals from HTML and the HTTP Link header
    Given the response header "Link" is '<https://ex.com/canonical-http>; rel="canonical"'
    And a page at URL "https://ex.com/p" with HTML:
      """
      <html><head><link rel="canonical" href="/canonical-html"></head><body></body></html>
      """
    Then the HTML canonical is "https://ex.com/canonical-html"
    And the HTTP canonical is "https://ex.com/canonical-http"

  Scenario: Hreflang annotations from HTML
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><head>
        <link rel="alternate" hreflang="en" href="https://ex.com/en">
        <link rel="alternate" hreflang="de-AT" href="https://ex.com/de-at">
      </head><body></body></html>
      """
    Then the page has 2 hreflang entries
    And hreflang "de-AT" points to "https://ex.com/de-at"

  Scenario: Pagination links
    Given a page at URL "https://ex.com/list?page=2" with HTML:
      """
      <html><head>
        <link rel="prev" href="?page=1">
        <link rel="next" href="?page=3">
      </head><body></body></html>
      """
    Then rel next is "https://ex.com/list?page=3"
    And rel prev is "https://ex.com/list?page=1"

  Scenario: Meta refresh is parsed and resolved
    Given a page at URL "https://ex.com/old" with HTML:
      """
      <html><head><meta http-equiv="refresh" content="0;url=/new"></head><body></body></html>
      """
    Then the meta refresh target is "https://ex.com/new"

  Scenario: base href changes link resolution
    Given a page at URL "https://ex.com/deep/dir/p" with HTML:
      """
      <html><head><base href="https://ex.com/other/"></head>
      <body><a href="page">x</a></body></html>
      """
    Then a hyperlink to "https://ex.com/other/page" exists

  Scenario: Word count respects the content area (nav and footer excluded by default)
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><body>
        <nav>skip these words entirely</nav>
        <p>one two three four five</p>
        <footer>and these too</footer>
      </body></html>
      """
    Then the word count is 5

  Scenario: Identical bodies hash identically
    Given a page at URL "https://ex.com/a" with HTML "<html><body>same</body></html>"
    And another page at URL "https://ex.com/b" with HTML "<html><body>same</body></html>"
    Then both pages have the same hash

  Scenario: Head and body validity problems are reported
    Given a page at URL "https://ex.com/p" with raw HTML "<html><body><p>no head</p></body><body></body></html>"
    Then the head validity check reports a missing head
    And the head validity check reports multiple body tags

  Scenario: The html lang attribute is captured
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html lang="de"><head><title>t</title></head><body></body></html>
      """
    Then the page language is "de"
