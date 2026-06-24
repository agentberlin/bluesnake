Feature: Crawl queue
  Crawls run through a single persistent queue, drained one at a time (no
  parallel crawls). The queue lives in the registry so it survives restarts, and
  a crawl left running by a crash is reconciled to interrupted — leaving its
  partial crawl resumable — rather than silently re-run.

  Scenario: Queued crawls run one at a time, in the order they were queued
    Given a fixture page "/a" and a fixture page "/b"
    When spider crawls of "/a" and "/b" are queued
    And the queue is drained
    Then both crawls complete in the registry
    And the crawls ran one at a time in the order they were queued

  Scenario: A crawl left running by a crash is reconciled, not re-run
    Given a job left running in the queue and a fresh job queued behind it
    When the queue is drained
    Then the abandoned job is marked interrupted
    And only the fresh job ran
