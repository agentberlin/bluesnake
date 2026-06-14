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
    And the crawl config override "resources.images.store=true"
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
    And the crawl config override "links.canonicals.crawl=true"
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

  # URL checks (uppercase, underscores, ...) are scoped to HTML pages: a
  # resource fetched via <a href> (here a GIF whose body Go sniffs as
  # image/gif) is a non-HTML page and is exempt, even though its URL has an
  # uppercase letter and an underscore. The HTML page with the same URL shape
  # is flagged. (Security-header checks require HTTPS and an oversized
  # image-as-page needs a multi-MB body the fixture server cannot cheaply
  # produce, so those facets of this fix stay unit-tested in
  # internal/issues/expansion_test.go.)
  Scenario: URL checks apply to HTML pages only, not resources fetched via a link
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>URL scope home page title here</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <a href="/Bad_Html">bad html url</a>
      <a href="/Pic_Image.gif">image via a link</a></body></html>
      """
    And a site page "/Bad_Html" with body:
      """
      <html lang="en"><head><title>Page with an uppercase underscore URL</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2></body></html>
      """
    And a test server route "/Pic_Image.gif" responding 200 with body "GIF89a sniffed as an image not html"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/Bad_Html" has issue "url_uppercase"
    And the page "/Bad_Html" has issue "url_underscores"
    And the crawl page "/Pic_Image.gif" has crawl state "crawled"
    And the page "/Pic_Image.gif" does not have issue "url_uppercase"
    And the page "/Pic_Image.gif" does not have issue "url_underscores"

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

  # Screaming Frog extracts two H1s per page (H1-1, H1-2) and its Duplicate
  # filter matches on either, so a single page whose two H1s are identical is
  # itself a Duplicate.
  Scenario: A page whose two H1s are identical is flagged as a duplicate
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Twin H1 heading home page title</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>Repeated heading text</h1><h1>Repeated heading text</h1><h2>sub</h2>
      <a href="/distinct-h1">distinct heading page</a></body></html>
      """
    And a site page "/distinct-h1" with body:
      """
      <html lang="en"><head><title>Distinct heading page title here</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>A unique first heading</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "h1_duplicate"
    And the page "/distinct-h1" does not have issue "h1_duplicate"

  Scenario: Duplicate and non-sequential H2 headings are flagged
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Heading order checks home page</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>Home</h1><h2>Shared section name</h2><h2>Shared section name</h2>
      <a href="/jumbled">jumbled headings page</a></body></html>
      """
    And a site page "/jumbled" with body:
      """
      <html lang="en"><head><title>Jumbled heading order page title</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>Title</h1><h3>Deep first</h3><h2>Then shallower</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    # / has two identical H2s -> Duplicate; its first H2 follows the H1 -> sequential.
    Then the page "/" has issue "h2_duplicate"
    And the page "/" does not have issue "h2_non_sequential"
    # /jumbled goes h1 -> h3 -> h2, so its first H2 follows a deeper heading.
    And the page "/jumbled" has issue "h2_non_sequential"
    And the page "/jumbled" does not have issue "h2_duplicate"

  Scenario: Missing structured data and directives outside the head are flagged
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Structured and directive checks page</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>Home</h1><h2>sub</h2>
      <meta name="robots" content="noarchive">
      <a href="/with-schema">page carrying schema</a></body></html>
      """
    And a site page "/with-schema" with body:
      """
      <html lang="en"><head><title>Page carrying valid JSON-LD schema</title>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <script type="application/ld+json">{"@type":"Product","name":"Widget","image":"i.jpg","offers":"o","review":"r","aggregateRating":"a"}</script>
      </head><body><h1>schema</h1><h2>sub</h2></body></html>
      """
    And the crawl config override "extraction.structured_data.jsonld=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "structured_missing"
    And the page "/" has issue "directive_outside_head"
    And the page "/with-schema" does not have issue "structured_missing"

  Scenario: Image checks are suppressed when images are not stored
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Unsized image with storage off</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2><img src="/unsized.png" alt="unsized image"></body></html>
      """
    And the crawl config override "resources.images.store=false"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "image_missing_size_attributes"

  Scenario: High external outlinks are reported only when external links are stored
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Page with many external outlinks</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <a href="https://ext-a.example/1">a</a>
      <a href="https://ext-b.example/2">b</a>
      <a href="https://ext-c.example/3">c</a>
      </body></html>
      """
    And the crawl config override "thresholds.high_external_outlinks=2"
    And the crawl config override "links.external.store=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "links_high_external_outlinks"

  Scenario: External outlink counts stay blank when external links are not stored
    Given a site page "/" with body:
      """
      <html lang="en"><head><title>Page with many external outlinks</title>
      <meta name="viewport" content="width=device-width, initial-scale=1"></head>
      <body><h1>h</h1><h2>sub</h2>
      <a href="https://ext-a.example/1">a</a>
      <a href="https://ext-b.example/2">b</a>
      <a href="https://ext-c.example/3">c</a>
      </body></html>
      """
    And the crawl config override "thresholds.high_external_outlinks=2"
    And the crawl config override "links.external.store=false"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "links_high_external_outlinks"
