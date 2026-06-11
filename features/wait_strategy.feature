Feature: Rendering wait strategy
  rendering.wait_strategy chooses how the JavaScript renderer decides when
  to snapshot the DOM: "adaptive" (the default) uses settle detection and
  releases the tab as soon as the page is quiet; "fixed" waits for the
  browser load event and then sleeps the full rendering.ajax_timeout_sec,
  giving deterministic snapshots for compare workflows.

  Scenario: Default wait strategy is adaptive
    Given a config file with contents ""
    When the config is loaded
    Then loading succeeds
    And the effective value of "rendering.wait_strategy" is "adaptive"

  Scenario: Fixed wait strategy is accepted by config validate
    Given a config file with contents "rendering: {wait_strategy: fixed}"
    When I run "bluesnake config validate <configfile>"
    Then the exit code is 0

  Scenario: Unknown wait strategy is rejected by config validate
    Given a config file with contents "rendering: {wait_strategy: eager}"
    When I run "bluesnake config validate <configfile>"
    Then the exit code is 2
    And the output contains "rendering.wait_strategy"

  Scenario: config init emits the adaptive default
    When I run "bluesnake config init --stdout"
    Then the exit code is 0
    And the output contains "wait_strategy: adaptive"
