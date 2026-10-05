Feature: Crawl bundle export
  A whole crawl exports as one self-describing, streamable JSON Lines file:
  a header record describing the crawl, then a record per site-check report
  and per llms.txt file, then one record per page carrying
  everything the crawl stored about it — its body text, response headers,
  structured data (including the raw JSON-LD blocks), custom search and
  extraction values, link-graph metrics, its nested link edges and, with
  --full, the page sources the crawl kept. The stream is versioned and counted so
  a consumer can refuse a format it does not understand and detect a
  truncated transfer, and byte-reproducible so two bundles of one crawl can
  be diffed.

  Background:
    Given a site page "/" with body:
      """
      <html><head><title>Bundle home page title</title>
      <link rel="stylesheet" href="/style.css">
      <script type="application/ld+json">{"@context":"https://schema.org","@type":"Organization","name":"BundleCo","logo":"l.png","url":"https://ex.com"}</script>
      </head><body>
        <h1>Bundle heading</h1>
        <p>uniquebundlemarker alpha bravo charlie delta echo foxtrot</p>
        <div class="site-footer"><a href="/about">About us</a></div>
      </body></html>
      """
    And a site page "/about" with body "<html><head><title>Bundle about page title</title></head><body><h1>About</h1><p>about body</p></body></html>"

  Scenario: The bundle command is discoverable
    When I run "bluesnake bundle --help"
    Then the exit code is 0
    And the output contains "--scope"
    And the output contains "--link-types"
    And the output contains "--full"

  # The header is the consumer's contract: `format` is what it pins on so it can
  # refuse a shape it does not understand, and `pages` is how it tells a
  # truncated transfer from a small crawl.
  Scenario: A header line describes the crawl and counts the pages that follow
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header has "format" equal to "bluesnake.pages/3"
    And the bundle header carries "bluesnake_version"
    And the bundle header carries "crawl_id"
    And the bundle header carries "config_digest"
    And the bundle page count matches the header

  # status_counts breaks the lines that follow down with the progress feed's six
  # keys and its classification, so a consumer can describe a crawl's outcomes
  # from line 1 alone. Every key is always present and the six sum to `pages`.
  Scenario: The header breaks the pages down by outcome
    Given a site page "/" linking to "/about, /missing"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header counts 2 pages as "status_2xx"
    And the bundle header counts 1 page as "status_4xx"
    And the bundle header counts 0 pages as "no_response"
    And the bundle header status counts add up to its pages

  # The single highest-value assertion in this file: page text is the field the
  # whole downstream index is built on, and the one no other export carries.
  Scenario: A crawled page carries its body text
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has "content_text" containing "uniquebundlemarker"
    And the bundle page "/" has "title" equal to "Bundle home page title"

  # The page sources are opt-in: --full carries them on every line when the
  # crawl kept them (its frozen config says so), and the header says both what
  # the crawl kept and that this file has it.
  Scenario: --full carries the stored HTML when the crawl kept it
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set extraction.store_html=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> --full -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header says "html" is stored
    And the bundle header has "full" equal to "true"
    And the bundle page "/" has "html" containing "<h1>Bundle heading</h1>"
    And the bundle page "/about" has "html" containing "<h1>About</h1>"

  # Without --full the sources stay out even when the crawl kept them — they are
  # most of the file by volume — and the header still says they were stored, so
  # a consumer knows a re-bundle, not a re-crawl, is what gets them the HTML.
  Scenario: Without --full the stored HTML stays out
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set extraction.store_html=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header says "html" is stored
    And the bundle header has "full" equal to "false"
    And the bundle page "/" has no "html" field

  # Nothing stored, nothing carried: the key is absent rather than "" on every
  # page, since "" could not say whether the source was never stored or stored
  # empty.
  Scenario: --full on a crawl that did not store HTML carries no html field
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> --full -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header says "html" is not stored
    And the bundle page "/" has no "html" field

  Scenario: A page carries its custom search and extraction values
    Given a config file with contents:
      """
      custom_search:
        - {name: marker, mode: contains, pattern: uniquebundlemarker}
      custom_extraction:
        - {name: heading, type: css, expression: h1}
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --config <configfile> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has custom result "marker" of kind "search" with value "1"
    And the bundle page "/" has custom result "heading" of kind "extraction" with value "Bundle heading"
    And the bundle page "/about" has custom result "marker" of kind "search" with value "0"

  # What a page withholds from search engines, by engine: a robots meta tag
  # addressed to one crawler (the generic meta_robots does not carry it) and
  # the text of its data-nosnippet elements.
  Scenario: A page carries its per-crawler robots meta tags and nosnippet text
    Given a site page "/" with body:
      """
      <html><head><title>Bundle snippet page title</title>
      <meta name="robots" content="index">
      <meta name="Googlebot" content="nosnippet">
      </head><body><h1>Snippets</h1>
      <p>shown <span data-nosnippet>withheld from snippets</span></p>
      </body></html>
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has a robots meta tag for "googlebot" with content "nosnippet"
    And the bundle page "/" has a data-nosnippet element with text "withheld from snippets"
    And the bundle page "/" has "meta_robots" equal to "[index]"

  # Every stored column rides along: the response headers as recorded, and the
  # link graph as finalize derived it.
  Scenario: A page carries its response headers and link graph
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has response header "Content-Type" containing "text/html"
    And the bundle page "/about" has "inlinks" equal to "1"
    And the bundle page "/about" has "discovered_from" equal to "<serverurl>/"

  # The site-check pass's reports are stored data, so they ride along, each on
  # a line of its own between the header and the pages, counted in the header:
  # the robots.txt the crawl obeyed, verbatim inside the robots report, and the
  # AI-bot verdicts, the search engines' crawlers among them. Their findings are
  # issues — verdicts — and stay out.
  Scenario: The bundle carries the site-check reports, robots.txt body included
    Given a robots.txt file:
      """
      User-agent: GPTBot
      Disallow: /

      User-agent: *
      Disallow: /private
      """
    And the test server serves the background robots.txt
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle has a "robots" site check whose report "body" contains "Disallow: /private"
    And the bundle has an "ai_bots" site check whose report "bots" contains "Googlebot"
    And the bundle page count matches the header

  # A report row exists exactly when a check ran, so a crawl run with the checks
  # off counts 0 of them rather than leaving the count out.
  Scenario: A crawl run with --site-checks off carries no site-check reports
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --site-checks off"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle has no site checks

  # Each page carries the sitemap entries that list it, with the lastmod each
  # gave it as written: honest last-updated dates, sitemap coverage (pages no
  # sitemap lists) and, with a null depth, orphans (listed, but no internal link
  # reaches them).
  Scenario: A page carries the sitemaps that list it, with their lastmod
    Given a robots.txt file:
      """
      User-agent: *
      Allow: /
      Sitemap: <serverurl>/sitemap.xml
      """
    And the test server serves the background robots.txt
    And a site page "/sitemap.xml" with body:
      """
      <?xml version="1.0" encoding="UTF-8"?>
      <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
        <url><loc><serverurl>/</loc><lastmod>2026-01-15</lastmod></url>
        <url><loc><serverurl>/orphan</loc></url>
      </urlset>
      """
    And a site page "/orphan" with body "<html><head><title>Bundle orphan page title</title></head><body><p>orphan</p></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" is listed in sitemap "/sitemap.xml" with lastmod "2026-01-15"
    And the bundle page "/orphan" is listed in sitemap "/sitemap.xml" with lastmod ""
    And the bundle page "/orphan" has a null "depth"
    And the bundle page "/about" is listed in no sitemap

  # alt is omitted when empty, so on its own it cannot tell a missing alt from a
  # decorative alt="". no_alt_attr can, on every image link.
  Scenario: An image link says whether its img had an alt attribute
    Given a site page "/" with body:
      """
      <html><head><title>Bundle images page title</title></head><body>
      <img src="/missing.png"><img src="/decorative.png" alt="">
      <a href="/about">About</a>
      </body></html>
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> --link-types hyperlink,image -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has a link to "/missing.png" with "no_alt_attr" equal to "true"
    And the bundle page "/" has a link to "/decorative.png" with "no_alt_attr" equal to "false"
    And the bundle page "/" has a link to "/about" with no "no_alt_attr" field

  Scenario: A page carries its author evidence, and an iframe its title
    Given a site page "/" with body:
      """
      <html><head><title>Bundle authors page title</title><meta name="author" content="Jane Doe"></head><body>
      <article><p class="byline">By <a href="/people/jane">Jane Doe</a></p>
      <iframe src="about:blank" data-src="/embed/intro" title="Intro video"></iframe></article>
      </body></html>
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> --link-types hyperlink,iframe -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has author evidence from "meta" named "Jane Doe"
    And the bundle page "/" has author evidence from "byline" named "By Jane Doe" linking "/people/jane"
    And the bundle page "/" has a link to "/embed/intro" with "title" equal to "Intro video"
    And the bundle page "/" has a link to "/embed/intro" with "raw" equal to "/embed/intro"
    And the bundle page "/" has a link to "/people/jane" with no "title" field

  # One record replaces the h1, h2 and heading_levels arrays (bluesnake.pages/2):
  # every level in document order, with its text, and whether an h1's text is
  # its image's alt.
  Scenario: A page carries its headings as one record
    Given a site page "/" with body:
      """
      <html><head><title>Bundle headings page title</title></head><body>
      <h1><img src="/logo.png" alt="Company Logo"></h1>
      <h2>Overview</h2><h4>Detail</h4><h2>Pricing</h2>
      </body></html>
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has headings:
      | level | text         | from_alt |
      | 1     | Company Logo | true     |
      | 2     | Overview     | false    |
      | 4     | Detail       | false    |
      | 2     | Pricing      | false    |

  Scenario: A JSON-LD block is emitted verbatim
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set extraction.structured_data.jsonld=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has structured jsonld containing "BundleCo"

  # A <div class="site-footer"> is only recognisable as furniture from the
  # id/class-annotated path; the pure-positional element path cannot say it.
  Scenario: A link carries the position path behind its label
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has a link to "/about" with "position" equal to "footer"
    And the bundle page "/" has a link to "/about" with "position_path" containing "site-footer"

  Scenario: Scope defaults to internal and --scope all includes external pages
    Given a second test server page "/page" linking onward to "/onward"
    And a site page "/" linking to "<external>/page"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set links.external.store=true --set links.external.crawl=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle contains no external page
    When I run "bluesnake bundle <crawlid> --store-dir <storedir> --scope all -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle contains an external page

  # A bundle's links are the link GRAPH; assets and references are the majority
  # of the table by volume and a consumer that wants them has the links tab.
  Scenario: Link types default to hyperlinks and --link-types all includes assets
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle contains no link of type "css"
    When I run "bluesnake bundle <crawlid> --store-dir <storedir> --link-types all -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle contains a link of type "css"

  Scenario: A .gz output writes a gzip stream
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set extraction.structured_data.jsonld=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl.gz"
    Then the exit code is 0
    And the file "crawl.jsonl.gz" in the store dir is a gzip stream containing "uniquebundlemarker"

  Scenario: Bundling twice produces identical bytes
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --set extraction.structured_data.jsonld=true"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/first.jsonl"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/second.jsonl"
    Then the exit code is 0
    And the files "first.jsonl" and "second.jsonl" in the store dir are identical
