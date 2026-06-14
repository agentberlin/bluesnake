Feature: Link extraction
  Every reference on a page becomes a typed link edge: hyperlinks, images,
  CSS, JavaScript, media, iframes, canonicals, hreflang, pagination, AMP,
  meta refresh, form actions. Edges carry anchor/alt text, rel, follow
  status, path type, element path and a configurable position label.

  Background:
    Given a page at URL "https://ex.com/dir/p" with HTML:
      """
      <html><head>
        <link rel="stylesheet" href="/style.css">
        <script src="https://cdn.ex.com/app.js"></script>
      </head><body>
        <nav><a href="/about">About us</a></nav>
        <main>
          <a href="next-page" rel="nofollow">Next page</a>
          <a href="https://other.com/x" rel="sponsored" target="_blank">Partner</a>
          <a href="/img-link"><img src="/pic.jpg" alt="A picture"></a>
          <img src="/plain.png" alt="">
          <iframe src="/frame.html"></iframe>
          <video src="/clip.mp4"></video>
          <form action="/search"></form>
        </main>
        <footer><a href="/imprint">Imprint</a></footer>
      </body></html>
      """

  Scenario: Hyperlinks carry anchor text and resolve relative hrefs
    Then a hyperlink to "https://ex.com/dir/next-page" exists with anchor "Next page"
    And a hyperlink to "https://ex.com/about" exists with anchor "About us"

  Scenario: nofollow, sponsored and ugc all mean not followed
    Then the link to "https://ex.com/dir/next-page" is nofollow
    And the link to "https://other.com/x" is nofollow
    And the link to "https://ex.com/about" is followed

  Scenario: Hyperlinked images use the image alt as anchor alt text
    Then a hyperlink to "https://ex.com/img-link" exists with alt "A picture"

  Scenario: Resources are typed
    Then a link of type "css" to "https://ex.com/style.css" exists
    And a link of type "js" to "https://cdn.ex.com/app.js" exists
    And a link of type "image" to "https://ex.com/plain.png" exists
    And a link of type "iframe" to "https://ex.com/frame.html" exists
    And a link of type "media" to "https://ex.com/clip.mp4" exists
    And a link of type "form_action" to "https://ex.com/search" exists

  Scenario: Link positions follow the configured rules
    Then the link to "https://ex.com/about" has position "nav"
    And the link to "https://ex.com/imprint" has position "footer"
    And the link to "https://ex.com/dir/next-page" has position "content"

  # The default link_positions use SF's decoded search terms: "header" is its
  # own segment ("/head/" only matches the document <head>). A <header> link
  # must classify as "header", not "head".
  Scenario: A header link is classified as header, not head
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><head><link rel="canonical" href="/canonical"></head>
      <body>
        <header><a href="/home">Home</a></header>
        <main><a href="/article">Article</a></main>
      </body></html>
      """
    Then the link to "https://ex.com/home" has position "header"
    And the link to "https://ex.com/article" has position "content"
    And the link to "https://ex.com/canonical" has position "head"
    # head links are //head-rooted by the same positional scheme as body links
    And the link to "https://ex.com/canonical" has element path "//head/link"

  # Screaming-Frog-style element paths: //body-rooted, 1-based same-tag
  # positional [k] indices, no id/class qualifiers.
  Scenario: Links carry Screaming-Frog-style positional element paths
    Given a page at URL "https://ex.com/p" with HTML:
      """
      <html><body><main><a href="/one">1</a><a href="/two">2</a></main></body></html>
      """
    Then the link to "https://ex.com/one" has element path "//body/main/a[1]"
    And the link to "https://ex.com/two" has element path "//body/main/a[2]"

  Scenario: Path types record how the href was written
    Then the link to "https://ex.com/about" has path type "root_relative"
    And the link to "https://ex.com/dir/next-page" has path type "path_relative"
    And the link to "https://other.com/x" has path type "absolute"

  Scenario: Target and rel attributes are stored
    Then the link to "https://other.com/x" has target "_blank"
    And the link to "https://other.com/x" has rel "sponsored"

  Scenario: Uncrawlable links are stored only when enabled
    Given a page at URL "https://ex.com/u" with HTML:
      """
      <html><body>
        <span href="/span-href">not a link</span>
        <a href="javascript:openMenu()">menu</a>
      </body></html>
      """
    Then the page has 0 links of type "uncrawlable"
    Given the parse config override "links.uncrawlable.store=true"
    And the page is re-parsed
    Then the page has 2 links of type "uncrawlable"

  Scenario: srcset candidates are extracted when enabled
    Given a page at URL "https://ex.com/s" with HTML:
      """
      <html><body><img src="/a.jpg" srcset="/a-2x.jpg 2x, /a-3x.jpg 3x"></body></html>
      """
    Then the page has 1 links of type "image"
    Given the parse config override "advanced.extract_srcset=true"
    And the page is re-parsed
    Then the page has 3 links of type "image"

  Scenario: AMP links are extracted
    Given a page at URL "https://ex.com/article" with HTML:
      """
      <html><head><link rel="amphtml" href="https://ex.com/article/amp"></head><body></body></html>
      """
    Then a link of type "amp" to "https://ex.com/article/amp" exists

  Scenario: Anchors without hrefs and unsupported schemes produce no edges
    Given a page at URL "https://ex.com/n" with HTML:
      """
      <html><body>
        <a name="top">anchor only</a>
        <a href="mailto:x@ex.com">mail</a>
        <a href="tel:+123">call</a>
      </body></html>
      """
    Then the page has 0 links of type "hyperlink"
