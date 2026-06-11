Feature: Hreflang ISO code validation
  Hreflang annotations must use assigned ISO 639-1 language codes, optionally
  followed by an assigned ISO 3166-1 alpha-2 region, or "x-default".
  Structurally well-formed but unassigned codes such as "zz-ZZ" are invalid.

  Scenario: Unassigned ISO codes are flagged while assigned codes pass
    Given a site page "/" with body:
      """
      <html><head>
        <title>Good hreflang home</title>
        <link rel="alternate" hreflang="en" href="<serverurl>/">
        <link rel="alternate" hreflang="en-US" href="<serverurl>/">
        <link rel="alternate" hreflang="x-default" href="<serverurl>/">
      </head><body><a href="/bad">bad page</a></body></html>
      """
    And a site page "/bad" with body:
      """
      <html><head>
        <title>Bad hreflang page</title>
        <link rel="alternate" hreflang="zz-ZZ" href="<serverurl>/bad">
      </head><body><a href="/">home</a></body></html>
      """
    When I crawl the site into a store
    And analysis is run
    Then the page "/bad" has issue "hreflang_invalid_code"
    And the page "/" does not have issue "hreflang_invalid_code"
