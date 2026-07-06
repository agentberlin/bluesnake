Feature: Crawl queue
  Crawls run through a single persistent queue, drained
  speed.max_concurrent_crawls at a time — 0 = unlimited, the default: every
  queued crawl runs immediately; a bound of n serializes beyond n. The
  width is live: retargeting it mid-drain applies immediately, with no
  restart. The queue lives in the registry so it survives restarts, and a
  crawl left running by a crash is reconciled to interrupted — leaving its
  partial crawl resumable — rather than silently re-run.

  Scenario: Queued crawls run one at a time, in the order they were queued
    Given a fixture page "/a" and a fixture page "/b"
    When spider crawls of "/a" and "/b" are queued
    And the queue is drained
    Then both crawls complete in the registry
    And the crawls ran one at a time in the order they were queued

  Scenario: The unlimited default runs every queued crawl at once
    Given a fixture page "/a" and a fixture page "/b"
    When spider crawls of "/a" and "/b" are queued on a parallel queue of 0
    And the queue is drained
    Then both crawls complete in the registry
    And the crawls ran concurrently

  Scenario: Parallel crawls drain concurrently when the queue allows two at once
    Given a fixture page "/a" and a fixture page "/b"
    When spider crawls of "/a" and "/b" are queued on a parallel queue of 2
    And the queue is drained
    Then both crawls complete in the registry
    And the crawls ran concurrently

  Scenario: Raising the parallel width mid-drain starts the waiting crawl immediately
    Given a fixture page "/a" and a fixture page "/b"
    When spider crawls of "/a" and "/b" are queued on a parallel queue of 1
    And the parallel width is raised to 2 while the first crawl runs
    And the queue is drained
    Then both crawls complete in the registry
    And the crawls ran concurrently

  Scenario: A crawl left running by a crash is reconciled, not re-run
    Given a job left running in the queue and a fresh job queued behind it
    When the queue is drained
    Then the abandoned job is marked interrupted
    And only the fresh job ran
