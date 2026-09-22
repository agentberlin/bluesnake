Feature: Crawl bundle export
  A whole crawl exports as one self-describing, streamable JSON Lines file:
  a header record describing the crawl, then one record per page carrying
  its body text, structured data (including the raw JSON-LD blocks) and its
  nested link edges. The stream is versioned and counted so a consumer can
  refuse a format it does not understand and detect a truncated transfer,
  and byte-reproducible so two bundles of one crawl can be diffed.

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

  # The header is the consumer's contract: `format` is what it pins on so it can
  # refuse a shape it does not understand, and `pages` is how it tells a
  # truncated transfer from a small crawl.
  Scenario: A header line describes the crawl and counts the pages that follow
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle header has "format" equal to "bluesnake.pages/1"
    And the bundle header carries "bluesnake_version"
    And the bundle header carries "crawl_id"
    And the bundle header carries "config_digest"
    And the bundle page count matches the header

  # The single highest-value assertion in this file: page text is the field the
  # whole downstream index is built on, and the one no other export carries.
  Scenario: A crawled page carries its body text
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake bundle <crawlid> --store-dir <storedir> -o <storedir>/crawl.jsonl"
    Then the exit code is 0
    And the bundle page "/" has "content_text" containing "uniquebundlemarker"
    And the bundle page "/" has "title" equal to "Bundle home page title"

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
