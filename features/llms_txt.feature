Feature: llms.txt site audit
  /llms.txt (llmstxt.org) is a site-level file, fetched once per host like
  robots.txt and validated structurally; its curated links are cross-checked
  against the crawl graph during analysis. Curated links are admitted to the
  frontier (so they get crawled and verified) unless crawl_linked is disabled.
  Every behaviour is a config knob under llms_txt.

  Scenario: A missing /llms.txt is flagged
    Given a site page "/" with body "<html><head><title>A home page with a fine title</title></head><body><h1>home</h1><p>hello world from the home page</p></body></html>"
    When I crawl the site into a store
    And analysis is run
    Then the page "/llms.txt" has issue "llms_txt_missing"

  Scenario: A valid /llms.txt validates its curated links against the crawl
    Given a site page "/" with body "<html><head><title>A home page with a fine title</title></head><body><h1>home</h1><p>hello world from the home page</p></body></html>"
    And a site page "/llms.txt" with body:
      """
      # Example Site

      > A short summary of the site for language models.

      ## Docs

      - [Guide](/guide): the user guide
      - [Gone](/gone)
      """
    And a site page "/guide" with body "<html><head><title>The guide page with a title</title></head><body><h1>guide</h1><p>plenty of guide words here for the reader</p></body></html>"
    When I crawl the site into a store
    And analysis is run
    Then the crawl page "/guide" has status code 200
    And the page "/gone" has issue "llms_txt_broken_link"
    And the page "/guide" does not have issue "llms_txt_broken_link"
    And the page "/llms.txt" does not have issue "llms_txt_invalid_format"

  Scenario: A malformed /llms.txt is flagged
    Given a site page "/" with body "<html><head><title>A home page with a fine title</title></head><body><h1>home</h1><p>hello world from the home page</p></body></html>"
    And a site page "/llms.txt" with body:
      """
      ## Docs

      - this line is not a well-formed link
      """
    When I crawl the site into a store
    And analysis is run
    Then the page "/llms.txt" has issue "llms_txt_invalid_format"
    And the page "/llms.txt" has issue "llms_txt_missing_summary"
    And the page "/llms.txt" has issue "llms_txt_malformed_link_list"

  Scenario: Disabling crawl_linked keeps curated links out of the frontier
    Given the crawl config override "llms_txt.crawl_linked=false"
    And a site page "/" with body "<html><head><title>A home page with a fine title</title></head><body><h1>home</h1><p>hello world from the home page</p></body></html>"
    And a site page "/llms.txt" with body:
      """
      # Example Site

      > A short summary of the site for language models.

      ## Docs

      - [Guide](/guide)
      """
    And a site page "/guide" with body "<html><head><title>The guide page with a title</title></head><body><h1>guide</h1><p>plenty of guide words here for the reader</p></body></html>"
    When I crawl the site into a store
    And analysis is run
    Then the page "/guide" has issue "llms_txt_link_unverified"

  Scenario: The whole audit can be turned off
    Given the crawl config override "llms_txt.check=false"
    And a site page "/" with body "<html><head><title>A home page with a fine title</title></head><body><h1>home</h1><p>hello world from the home page</p></body></html>"
    When I crawl the site into a store
    And analysis is run
    Then the page "/llms.txt" does not have issue "llms_txt_missing"
