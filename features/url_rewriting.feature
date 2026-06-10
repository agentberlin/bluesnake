Feature: URL rewriting
  Discovered URLs (never the start/list URLs) can be rewritten before queueing:
  named query parameters removed, regex replacements applied in order, optional
  lowercasing. Mirrors Screaming Frog's URL Rewriting config.

  Scenario: Removing tracking parameters
    Given remove_params is configured with "utm_source, utm_medium, sessionid"
    When the discovered URL "https://ex.com/p?utm_source=x&id=2&sessionid=abc" is rewritten
    Then the result is "https://ex.com/p?id=2"

  Scenario: Removing every parameter leaves a clean URL
    Given remove_params is configured with "q"
    When the discovered URL "https://ex.com/p?q=1" is rewritten
    Then the result is "https://ex.com/p"

  Scenario: Regex replacements apply in order to every match
    Given regex_replace is configured with:
      | pattern        | replace |
      | /old/          | /new/   |
      | ^http://       | https:// |
    When the discovered URL "http://ex.com/old/page" is rewritten
    Then the result is "https://ex.com/new/page"

  Scenario: Capture group backreferences
    Given regex_replace is configured with:
      | pattern              | replace |
      | /product/(\d+)/.+    | /product/$1 |
    When the discovered URL "https://ex.com/product/42/blue-shirt" is rewritten
    Then the result is "https://ex.com/product/42"

  Scenario: Lowercasing discovered URLs
    Given lowercase rewriting is enabled
    When the discovered URL "https://ex.com/Some/Path?Q=Mixed" is rewritten
    Then the result is "https://ex.com/some/path?q=mixed"

  Scenario: Rewriting is not applied to start URLs
    Given remove_params is configured with "id"
    When the start URL "https://ex.com/p?id=2" is prepared
    Then the result is "https://ex.com/p?id=2"
