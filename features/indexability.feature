Feature: Indexability
  Every URL gets an Indexability verdict (Indexable / Non-Indexable) with a
  reason, evaluated in fixed precedence order: blocked by robots.txt, then
  no response, then 4xx/5xx, then redirects, then noindex, then
  canonicalised-elsewhere.

  Scenario: A plain 200 HTML page is indexable
    Given a URL "https://ex.com/p" with status 200
    When indexability is evaluated
    Then the URL is indexable

  Scenario: Robots-blocked URLs are non-indexable
    Given a URL "https://ex.com/p" with status 0
    And the URL is blocked by robots.txt
    When indexability is evaluated
    Then the URL is non-indexable with reason "Blocked by Robots.txt"

  Scenario: Network failures are non-indexable
    Given a URL "https://ex.com/p" with a fetch error
    When indexability is evaluated
    Then the URL is non-indexable with reason "No Response"

  Scenario Outline: Error statuses are non-indexable
    Given a URL "https://ex.com/p" with status <status>
    When indexability is evaluated
    Then the URL is non-indexable with reason "<reason>"

    Examples:
      | status | reason       |
      | 404    | Client Error |
      | 410    | Client Error |
      | 500    | Server Error |
      | 503    | Server Error |

  Scenario: Redirects are non-indexable
    Given a URL "https://ex.com/old" with status 301
    When indexability is evaluated
    Then the URL is non-indexable with reason "Redirected"

  Scenario: Meta refresh to another URL is a redirect
    Given a URL "https://ex.com/old" with status 200
    And a meta refresh target "https://ex.com/new"
    When indexability is evaluated
    Then the URL is non-indexable with reason "Redirected"

  Scenario: A self-referencing meta refresh makes the page non-indexable by default
    Given a URL "https://ex.com/p" with status 200
    And a meta refresh target "https://ex.com/p"
    When indexability is evaluated
    Then the URL is non-indexable with reason "Redirected"

  Scenario: Self-referencing meta refresh can be tolerated by config
    Given a URL "https://ex.com/p" with status 200
    And a meta refresh target "https://ex.com/p"
    And the indexability config override "advanced.respect_self_referencing_meta_refresh=false"
    When indexability is evaluated
    Then the URL is indexable

  Scenario Outline: Noindex directives from meta robots or X-Robots-Tag
    Given a URL "https://ex.com/p" with status 200
    And <source> contains "<value>"
    When indexability is evaluated
    Then the URL is non-indexable with reason "Noindex"

    Examples:
      | source       | value            |
      | meta robots  | noindex          |
      | meta robots  | noindex, follow  |
      | meta robots  | none             |
      | x-robots-tag | noindex          |

  Scenario: UA-scoped X-Robots-Tag only applies to our robots user-agent
    Given a URL "https://ex.com/p" with status 200
    And x-robots-tag contains "otherbot: noindex"
    When indexability is evaluated
    Then the URL is indexable

  Scenario: Canonicalised pages are non-indexable
    Given a URL "https://ex.com/variant" with status 200
    And a canonical pointing to "https://ex.com/main"
    When indexability is evaluated
    Then the URL is non-indexable with reason "Canonicalised"

  Scenario: A self-referencing canonical stays indexable
    Given a URL "https://ex.com/p" with status 200
    And a canonical pointing to "https://ex.com/p"
    When indexability is evaluated
    Then the URL is indexable

  Scenario: Canonical comparison is normalization-aware
    Given a URL "https://ex.com/p" with status 200
    And a canonical pointing to "https://EX.com:443/p"
    When indexability is evaluated
    Then the URL is indexable

  Scenario: Noindex takes precedence over canonicalised
    Given a URL "https://ex.com/p" with status 200
    And meta robots contains "noindex"
    And a canonical pointing to "https://ex.com/other"
    When indexability is evaluated
    Then the URL is non-indexable with reason "Noindex"
