Feature: Live progress for headless crawls
  An unattended crawl (a container, a CI job) streams its live progress with
  --progress json: one JSON object per line on stderr, each with
  "type":"progress". There is a record when the crawl starts, one every
  --progress-interval while it runs, and a final record with the terminal
  state. stdout carries exactly what it does without the flag, so the summary
  and the "Crawl ID:" line stay where scripts parse them. These scenarios put
  stdout and stderr on separate pipes, as a container runtime does.

  Scenario: Progress streams on stderr while stdout is unchanged
    Given a site page "/" linking to "/a,/b"
    And a site page "/a" linking to ""
    And a site page "/b" linking to ""
    And a test server route "/b" that sleeps 1500ms before responding 200
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --setup defaults --progress json --progress-interval 1s" with stdout and stderr apart
    Then the exit code is 0
    And stdout contains "Found 3 URLs"
    And every stderr line is a progress record
    And the progress records carry the crawl ID printed on stdout
    And the first progress record has "state" equal to "running"
    # /b is still in flight when the 1s tick fires
    And a mid-crawl progress record reports at least 1 processed
    And the last progress record has "state" equal to "completed"
    And the last progress record has "processed" equal to "3"
    And the last progress record has "discovered" equal to "3"
    And the last progress record has "status_2xx" equal to "3"
    And the last progress record has "status_4xx" equal to "0"
    And the last progress record has "no_response" equal to "0"

  Scenario: A progress bar off a terminal writes a plain line per reading
    # On a terminal --progress bar redraws a panel in place (a bar split by
    # status class, each class's status codes, the last minute's rate and
    # response times); on a pipe the redraws would pile up, so each reading is
    # one line of its own, ending in the status classes seen so far.
    Given a site page "/" linking to "/a,/b"
    And a site page "/a" linking to ""
    And a site page "/b" linking to ""
    And a test server route "/b" that sleeps 1500ms before responding 200
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --setup defaults --progress bar --progress-interval 1s" with stdout and stderr apart
    Then the exit code is 0
    And stdout contains "Found 3 URLs"
    And stderr is whole lines with no carriage returns
    And a stderr line other than the last contains " left "
    And the last stderr line contains "100%  3/3  done in"
    And the last stderr line contains "  ·  2xx 3"

  Scenario: Without --progress nothing is written to stderr
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir>" with stdout and stderr apart
    Then the exit code is 0
    And stdout contains "Crawl ID:"
    And stderr is empty

  Scenario: --quiet silences the summary, not the progress feed
    Given a site page "/" linking to "/a"
    And a site page "/a" linking to ""
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --quiet --progress json" with stdout and stderr apart
    Then the exit code is 0
    And stdout is empty
    And every stderr line is a progress record
    And the last progress record has "state" equal to "completed"
    And the last progress record has "processed" equal to "2"

  Scenario: A resumed crawl's progress counts the whole crawl
    # The status breakdown carries over from the first session like the
    # processed count does, so it always adds up to processed.
    Given a stored crawl of a 40-page fixture site interrupted after 10 pages
    When I run "bluesnake resume <crawlid> --store-dir <storedir> --progress json" with stdout and stderr apart
    Then the exit code is 0
    And every stderr line is a progress record
    And the first progress record has "processed" equal to "10"
    And the first progress record has "status_2xx" equal to "10"
    And the last progress record has "state" equal to "completed"
    And the last progress record has "processed" equal to "41"
    And the last progress record has "status_2xx" equal to "41"

  Scenario: Unusable progress flags are config errors
    Given a site page "/" linking to ""
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --progress xml" with stdout and stderr apart
    Then the exit code is 2
    And stdout is empty
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --progress json --progress-interval 100ms" with stdout and stderr apart
    Then the exit code is 2
    And stdout is empty
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --progress bar --progress-interval 100ms" with stdout and stderr apart
    Then the exit code is 2
    And stdout is empty
    When I run "bluesnake crawl <serverurl>/ --store-dir <storedir> --progress-interval 30s" with stdout and stderr apart
    Then the exit code is 2
    And stdout is empty
