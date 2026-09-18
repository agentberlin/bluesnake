Feature: Plain-text configuration
  bluesnake is configured by a single YAML file plus CLI overrides.
  An empty config is valid: every key has a documented default.
  Unknown keys and invalid values are hard errors at load time.

  Scenario: Empty config yields full defaults
    Given a config file with contents ""
    When the config is loaded
    Then loading succeeds
    And the effective value of "speed.max_threads" is "5"
    And the effective value of "limits.max_urls" is "5000000"
    And the effective value of "robots.mode" is "respect"
    And the effective value of "advanced.response_timeout_sec" is "20"
    And the effective value of "advanced.respect_hsts" is "true"
    And the effective value of "rendering.mode" is "text"
    And the effective value of "content.near_duplicates.threshold" is "90"
    And the effective value of "thresholds.title.max_chars" is "60"
    And the effective value of "links.pagination.crawl" is "false"
    And the effective value of "links.hreflang.store" is "true"
    And the effective value of "links.hreflang.crawl" is "false"
    And the effective value of "http.browser_headers" is "true"
    And the effective value of "http.version" is ""

  Scenario: Partial config merges over defaults
    Given a config file with contents:
      """
      speed:
        max_threads: 12
      limits:
        max_depth: 3
      """
    When the config is loaded
    Then loading succeeds
    And the effective value of "speed.max_threads" is "12"
    And the effective value of "limits.max_depth" is "3"
    And the effective value of "limits.max_urls" is "5000000"

  Scenario: Unknown keys are rejected
    Given a config file with contents:
      """
      sped:
        max_threads: 12
      """
    When the config is loaded
    Then loading fails with an error containing "sped"

  Scenario: Invalid regex in exclude is rejected at load time
    Given a config file with contents:
      """
      scope:
        exclude: ["[unclosed"]
      """
    When the config is loaded
    Then loading fails with an error containing "exclude"

  Scenario: Invalid enum value is rejected
    Given a config file with contents:
      """
      robots:
        mode: obey
      """
    When the config is loaded
    Then loading fails with an error containing "robots.mode"

  Scenario: Invalid HTTP version is rejected
    Given a config file with contents:
      """
      http:
        version: "3"
      """
    When the config is loaded
    Then loading fails with an error containing "http.version"

  Scenario: The single-proxy shorthand and the proxy list cannot both be set
    Given a config file with contents:
      """
      http:
        proxy: http://a:8080
        proxies:
          - url: http://b:8080
      """
    When the config is loaded
    Then loading fails with an error containing "mutually exclusive"

  Scenario: A proxy entry without a URL is rejected
    Given a config file with contents:
      """
      http:
        proxies:
          - max_concurrent: 4
      """
    When the config is loaded
    Then loading fails with an error containing "http.proxies[0].url"

  Scenario: An invalid proxy strategy is rejected
    Given a config file with contents:
      """
      http:
        proxy_strategy: spiral
      """
    When the config is loaded
    Then loading fails with an error containing "http.proxy_strategy"

  Scenario: Spreading requests across proxies is refused when one session is reused
    Given a config file with contents:
      """
      advanced:
        cookie_storage: persistent
      http:
        proxy_strategy: round_robin
        proxies:
          - url: http://a:8080
          - url: http://b:8080
      """
    When the config is loaded
    Then loading fails with an error containing "shared identity"

  Scenario: Out-of-range threshold is rejected
    Given a config file with contents:
      """
      content:
        near_duplicates:
          threshold: 150
      """
    When the config is loaded
    Then loading fails with an error containing "threshold"

  Scenario: Dotted overrides take precedence over file values
    Given a config file with contents:
      """
      speed:
        max_threads: 12
      """
    And the override "speed.max_threads=3"
    And the override "scope.crawl_all_subdomains=true"
    When the config is loaded
    Then loading succeeds
    And the effective value of "speed.max_threads" is "3"
    And the effective value of "scope.crawl_all_subdomains" is "true"

  Scenario: config init emits a default file that round-trips
    When I run "bluesnake config init --stdout"
    Then the exit code is 0
    And the output is a valid config that loads with all default values

  Scenario: config validate reports success for a valid file
    Given a config file with contents "speed: {max_threads: 2}"
    When I run "bluesnake config validate <configfile>"
    Then the exit code is 0

  Scenario: config validate reports failure for an invalid file
    Given a config file with contents "speed: {max_threads: -2}"
    When I run "bluesnake config validate <configfile>"
    Then the exit code is 2
    And the output contains "max_threads"
