Feature: Crawl setup sources
  A site remembers the setup it last ran with: by default a new crawl of the
  same site freezes the same configuration again — derived at enqueue time
  from the site's most recent spider crawl, never stored per site. Sites that
  were never crawled fall back to the app settings (the saved default
  profile), and a named profile overrides the remembered setup explicitly.
  Site identity is the exact lowercased host:port of the seed: www, other
  subdomains and ports are different sites.

  Scenario: A site's next crawl reuses its last setup by default
    Given a site was crawled with max depth 1
    When a crawl of the same site is enqueued with the last-used setup
    Then the enqueued job freezes max depth 1

  Scenario: A never-crawled site falls back to the app settings
    Given the app settings set max depth 4
    When a crawl of an uncrawled site is enqueued with the last-used setup
    Then the enqueued job freezes max depth 4

  Scenario: A named profile overrides the remembered setup
    Given a site was crawled with max depth 1
    And a profile "Deep" with max depth 9
    When a crawl of the same site is enqueued with profile "Deep"
    Then the enqueued job freezes max depth 9

  Scenario: The www subdomain is a different site and remembers nothing
    Given a site was crawled with max depth 1
    When a crawl of the www subdomain of the site is enqueued with the last-used setup
    Then the enqueued job freezes the default max depth
