Feature: Structured data
  JSON-LD, Microdata and RDFa extraction with validation of Google
  rich-result requirements (curated common-feature subset): missing required
  properties are errors, missing recommended properties are warnings, and
  unparseable JSON-LD is a parse error.

  Scenario: Valid and invalid structured data through a real crawl
    Given a site page "/" with body:
      """
      <html><head><title>Products page with a fine title</title>
      <script type="application/ld+json">{"@type":"Product","name":"Widget","image":"i.jpg","offers":"x","review":"r","aggregateRating":"a"}</script>
      </head><body><h1>p</h1><h2>s</h2><a href="/bad">bad</a></body></html>
      """
    And a site page "/bad" with body:
      """
      <html><head><title>Recipe missing its required props</title>
      <script type="application/ld+json">{"@type":"Recipe","recipeIngredient":["x"]}</script>
      </head><body><h1>b</h1><h2>s</h2></body></html>
      """
    And the crawl config override "extraction.structured_data.jsonld=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/bad" has issue "structured_validation_error"
    And the page "/" does not have issue "structured_validation_error"
