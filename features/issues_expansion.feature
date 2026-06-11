Feature: Issue detection — catalogue expansion
  New SF-parity checks: mobile viewport, html lang, image size attributes,
  outlinks to redirecting/broken pages, redirects to broken pages and
  canonicals pointing at redirects. The full matrix (charset header logic,
  insecure cookies, inlink aggregates) is unit-tested in
  internal/issues/expansion_test.go; these scenarios prove end-to-end
  detection through a real crawl.

  Scenario: Pages without a viewport or html lang attribute are flagged
    Given a site page "/" with body:
      """
      <html><head><title>Desktop only home page title</title></head>
      <body><h1>home</h1><h2>sub</h2><a href="/mobile-ready">mobile ready page</a></body></html>
      """
    And a site page "/mobile-ready" with body:
      """
      <html lang="en"><head><title>Mobile ready page title here</title>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      </head><body><h1>m</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "viewport_missing"
    And the page "/" has issue "html_lang_missing"
    And the page "/mobile-ready" does not have issue "viewport_missing"
    And the page "/mobile-ready" does not have issue "html_lang_missing"

  Scenario: Images without size attributes are flagged on the embedding page
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Image size attributes fixture</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <img src="/unsized.png" alt="unsized image">
      <img src="/sized.png" alt="sized image" width="100" height="80">
      <a href="/all-sized">all sized page</a>
      </body></html>
      """
    And a site page "/all-sized" with body:
      """
      <html lang="en"><head><title>All images carry size attributes</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <img src="/sized.png" alt="sized image" width="100" height="80">
      </body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "image_missing_size_attributes"
    And the page "/all-sized" does not have issue "image_missing_size_attributes"

  Scenario: Outlinks to redirecting or broken pages, and redirects to broken pages
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Linking fixture home page title</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <a href="/moved">moved page</a>
      <a href="/dead-redirect">dead redirect page</a>
      <a href="/vanished">vanished page</a>
      <a href="/well-behaved">well behaved page</a>
      </body></html>
      """
    And a test server route "/moved" redirecting 301 to "/healthy"
    And a test server route "/dead-redirect" redirecting 301 to "/vanished"
    And a site page "/healthy" with body "<html lang='en'><head><title>A healthy target page title</title></head><body><h1>h</h1><h2>sub</h2></body></html>"
    And a site page "/well-behaved" with body "<html lang='en'><head><title>Page linking only to live pages</title></head><body><h1>h</h1><h2>sub</h2><a href='/healthy'>healthy page</a></body></html>"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "links_outlinks_to_redirect"
    And the page "/" has issue "links_outlinks_to_broken"
    And the page "/well-behaved" does not have issue "links_outlinks_to_redirect"
    And the page "/well-behaved" does not have issue "links_outlinks_to_broken"
    And the page "/dead-redirect" has issue "redirect_broken"
    And the page "/moved" does not have issue "redirect_broken"

  Scenario: A canonical pointing at a redirect is flagged
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Canonical redirect fixture home</title>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <link rel="canonical" href="/moved-canon">
      </head><body><h1>h</h1><h2>sub</h2><a href="/self-canon">self canonical page</a></body></html>
      """
    And a test server route "/moved-canon" redirecting 301 to "/final-canon"
    And a site page "/final-canon" with body "<html lang='en'><head><title>The final canonical target page</title></head><body><h1>h</h1><h2>sub</h2></body></html>"
    And a site page "/self-canon" with body:
      """
      <html lang="en"><head><title>Self canonical page title here</title>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <link rel="canonical" href="/self-canon">
      </head><body><h1>h</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "canonical_to_redirect"
    And the page "/self-canon" does not have issue "canonical_to_redirect"

  # The fixture server never sets Content-Type itself, so Go's net/http
  # sniffs HTML bodies and always serves "text/html; charset=utf-8". The
  # charset= token in the header means charset_missing can never fire here
  # even without a <meta charset> tag — which is itself worth pinning. The
  # positive case (no meta AND no charset in the header) is unit-tested in
  # internal/issues/expansion_test.go, as is security_insecure_cookie, which
  # needs Set-Cookie response headers the fixture server cannot produce.
  Scenario: A charset declared only in the Content-Type header satisfies the check
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>No meta charset on this page</title>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      </head><body><h1>h</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "charset_missing"
