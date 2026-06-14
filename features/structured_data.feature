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

  # Google lists headline as a recommended (not required) Article property, so
  # a missing headline is a Rich Result Validation warning, never an error.
  Scenario: A missing recommended Article property warns rather than errors
    Given a site page "/" with body:
      """
      <html><head><title>Article without a headline property</title>
      <script type="application/ld+json">{"@type":"Article","image":"i.jpg","datePublished":"2026-01-01","author":"A"}</script>
      </head><body><h1>a</h1><h2>s</h2></body></html>
      """
    And the crawl config override "extraction.structured_data.jsonld=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" has issue "structured_validation_warning"
    And the page "/" does not have issue "structured_validation_error"

  # An Organization is a Logo rich-result candidate only when it carries a
  # logo: logo-less boilerplate is not eligible and emits nothing, while a
  # logo-bearing Organization is validated against its recommended properties.
  Scenario: A logo-less Organization is not validated, a logo-bearing one is
    Given a site page "/" with body:
      """
      <html><head><title>Logo-less organization boilerplate page</title>
      <script type="application/ld+json">{"@type":"Organization","name":"Acme Corp"}</script>
      </head><body><h1>a</h1><h2>s</h2><a href="/branded">branded page</a></body></html>
      """
    And a site page "/branded" with body:
      """
      <html><head><title>Organization with a logo but no url</title>
      <script type="application/ld+json">{"@type":"Organization","name":"Acme Corp","logo":"logo.png"}</script>
      </head><body><h1>b</h1><h2>s</h2></body></html>
      """
    And the crawl config override "extraction.structured_data.jsonld=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "structured_validation_warning"
    And the page "/" does not have issue "structured_validation_error"
    And the page "/branded" has issue "structured_validation_warning"

  # A raw control character inside a JSON-LD string literal is escaped and
  # re-parsed (Google's/SF's lenient parser) instead of being reported as a
  # parse error. The headline below contains a literal TAB.
  Scenario: JSON-LD with a raw control character is recovered, not a parse error
    Given a site page "/" with body:
      """
      <html><head><title>Article JSON-LD with a raw control char</title>
      <script type="application/ld+json">{"@type":"Article","headline":"Line one	line two","image":"i.jpg","datePublished":"2026-01-01","author":"A"}</script>
      </head><body><h1>a</h1><h2>s</h2></body></html>
      """
    And the crawl config override "extraction.structured_data.jsonld=true"
    When I crawl the site into a store
    And issues are evaluated
    Then the page "/" does not have issue "structured_parse_error"
    And the page "/" does not have issue "structured_missing"
