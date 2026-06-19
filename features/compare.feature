Feature: Crawl comparison
  Two stored crawls diff into per-issue deltas using Screaming Frog's four
  buckets (Added/New/Removed/Missing) plus element-level change detection.

  Scenario: Comparing two crawls of a changed site
    Given a site page "/" linking to "/stable,/fixed"
    And a site page "/stable" with body "<html><head><title>A stable page title here ok</title></head><body><h1>s</h1><h2>x</h2></body></html>"
    And a site page "/fixed" with body "<html><body><h1>no title yet</h1></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And the site page "/fixed" changes to body "<html><head><title>Now this page has a title</title></head><body><h1>f</h1><h2>x</h2></body></html>"
    And I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake compare <firstcrawlid> <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "title_missing"
    And the output contains "removed"
    And the output contains "Pages: 3 -> 3"

  # The content area (nav/footer excluded) is compared with the shared minhash
  # similarity; a materially rewritten body crosses the default >10% threshold.
  Scenario: A page's content change is detected
    Given a site page "/" linking to "/article"
    And a site page "/article" with body "<html><head><title>An unchanging article headline here</title></head><body><p>Spring planting begins once the soil warms and the last frost has finally passed.</p></body></html>"
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And the site page "/article" changes to body "<html><head><title>An unchanging article headline here</title></head><body><p>Container hydroponics lets urban growers raise crisp lettuce indoors without any garden soil.</p></body></html>"
    And I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet"
    And I run "bluesnake compare <firstcrawlid> <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "changed content"
    And the output contains "/article"

  # Structured data is compared by its unique schema.org type set; swapping the
  # JSON-LD @type from Article to Product is a Structured Data change.
  Scenario: A change in structured data types is detected
    Given a site page "/" linking to "/p"
    And a site page "/p" with body:
      """
      <html><head><title>A page carrying structured data markup</title>
      <script type="application/ld+json">{"@type":"Article","headline":"x"}</script>
      </head><body><h1>p</h1></body></html>
      """
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --set extraction.structured_data.jsonld=true --quiet"
    And the site page "/p" changes to body:
      """
      <html><head><title>A page carrying structured data markup</title>
      <script type="application/ld+json">{"@type":"Product","name":"x"}</script>
      </head><body><h1>p</h1></body></html>
      """
    And I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --set extraction.structured_data.jsonld=true --quiet"
    And I run "bluesnake compare <firstcrawlid> <crawlid> --store-dir <storedir>"
    Then the exit code is 0
    And the output contains "changed structured_data"
    And the output contains "Article"
    And the output contains "Product"
