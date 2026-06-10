Feature: Include and exclude patterns
  Include/exclude are partial-match RE2 patterns evaluated against the
  URL-encoded address of *discovered* URLs. Excluded URLs are never requested,
  so pages reachable only through them are never found. The start URL is exempt.

  Scenario: No patterns means everything passes
    Given no include or exclude patterns
    Then the discovered URL "https://ex.com/anything" is allowed

  Scenario: Include restricts the crawl
    Given include patterns:
      | /blog/ |
    Then the discovered URL "https://ex.com/blog/post" is allowed
    And the discovered URL "https://ex.com/shop/item" is denied

  Scenario: Multiple includes are OR-ed
    Given include patterns:
      | /blog/ |
      | /docs/ |
    Then the discovered URL "https://ex.com/docs/x" is allowed
    And the discovered URL "https://ex.com/shop/x" is denied

  Scenario: Exclude wins over include
    Given include patterns:
      | /blog/ |
    And exclude patterns:
      | /blog/private/ |
    Then the discovered URL "https://ex.com/blog/post" is allowed
    And the discovered URL "https://ex.com/blog/private/x" is denied

  Scenario: Matching happens on the URL-encoded address
    Given exclude patterns:
      | caf%C3%A9 |
    Then the discovered URL "https://ex.com/café" is denied

  Scenario: Patterns are partial match, not anchored
    Given exclude patterns:
      | \?page= |
    Then the discovered URL "https://ex.com/list?page=2" is denied
    And the discovered URL "https://ex.com/list" is allowed

  Scenario: The start URL bypasses include/exclude
    Given exclude patterns:
      | .* |
    Then the start URL "https://ex.com/" is allowed
