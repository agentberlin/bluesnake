Feature: SERP pixel widths
  Titles and meta descriptions are measured in pixels with bundled Arial
  font metrics (title at 20px, description at 13px — Google desktop SERP
  rendering) in addition to character counts. The thresholds.title and
  thresholds.description min_px/max_px settings drive the Over/Below X
  Pixels issues, so a title can be within its character budget yet still
  truncate (wide glyphs) or look thin (narrow glyphs) on the results page.

  Scenario: A wide title within its character budget still overflows the pixel budget
    Given a site page "/" with body:
      """
      <html><head><title>WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW</title>
      <meta name="description" content="A perfectly reasonable description for a wide page that says enough words to pass the minimum character thresholds easily.">
      </head><body><h1>wide</h1><h2>sub</h2><a href="/narrow">next page</a></body></html>
      """
    And a site page "/narrow" with body:
      """
      <html><head><title>llllllllllllllllllllllllllllllllllllllll</title>
      <meta name="description" content="Another perfectly reasonable description for the narrow page, long enough to pass the minimum character thresholds too.">
      </head><body><h1>narrow</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "title_over_pixels"
    And the page "/" does not have issue "title_over_chars"
    And the page "/narrow" has issue "title_below_pixels"
    And the page "/narrow" does not have issue "title_below_chars"

  Scenario: Descriptions are measured at the description font size
    Given a site page "/" with body:
      """
      <html><head><title>A page with an entirely ordinary title</title>
      <meta name="description" content="WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW">
      </head><body><h1>w</h1><h2>sub</h2><a href="/thin">next</a></body></html>
      """
    And a site page "/thin" with body:
      """
      <html><head><title>Another page with an ordinary title</title>
      <meta name="description" content="iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii">
      </head><body><h1>t</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "description_over_pixels"
    And the page "/" does not have issue "description_over_chars"
    And the page "/thin" has issue "description_below_pixels"
    And the page "/thin" does not have issue "description_below_chars"

  # Pin the description font size (13.9px). The plain over-pixels check stays
  # green even if the size regresses to 13.0px, so assert the measured width:
  # 100 'W' glyphs measure differently at 13.9px than at any other size.
  Scenario: The description pixel width is measured at the 13.9px description font
    Given a site page "/" with body:
      """
      <html><head><title>A page with an entirely ordinary title</title>
      <meta name="description" content="WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW">
      </head><body><h1>w</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "description_over_pixels" with detail "1338px"

  Scenario: A normal title and description raise no pixel issues
    Given a site page "/" with body:
      """
      <html><head><title>A sensible product page title for sale</title>
      <meta name="description" content="A sensible meta description that fits the pixel budget comfortably while still saying something useful about the page.">
      </head><body><h1>product</h1><h2>sub</h2></body></html>
      """
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "title_over_pixels"
    And the page "/" does not have issue "title_below_pixels"
    And the page "/" does not have issue "description_over_pixels"
    And the page "/" does not have issue "description_below_pixels"
