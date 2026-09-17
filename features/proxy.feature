Feature: Proxy egress
  Requests leave through a configured egress — one proxy, a pool of them, or
  the machine's own IP — chosen per request. Which egress served a page is
  recorded with the page, because a crawl that a firewall partially blocked
  cannot be diagnosed afterwards without it. Credentials reach the proxy and
  nothing else: never a result, an export, a log or the UI.

  Scenario: Without a proxy configured, requests are recorded as direct
    Given a test server route "/p" responding 200 with body "hello"
    When I fetch "/p"
    Then the fetch status code is 200
    And the fetch records a proxy

  Scenario: A single configured proxy carries every request
    Given a test server route "/p" responding 200 with body "hello"
    And 1 test proxies
    When I fetch "/p"
    Then the fetch status code is 200
    And the fetch body is "hello"
    And the fetch went through test proxy 1
    And test proxy 1 served 1 requests

  Scenario: Credentials never appear in the recorded egress
    Given a test server route "/p" responding 200 with body "hello"
    And 1 test proxies
    When I fetch "/p"
    Then the fetch records a proxy
    And the recorded proxy contains no credentials

  Scenario: Requests are spread across a pool of proxies
    Given a test server route "/a" responding 200 with body "a"
    And a test server route "/b" responding 200 with body "b"
    And 2 test proxies
    When I fetch "/a"
    Then the fetch went through test proxy 1
    When I fetch "/b"
    Then the fetch went through test proxy 2
    And test proxy 1 served 1 requests
    And test proxy 2 served 1 requests

  Scenario: Pinning one egress per host keeps a shared session behind one IP
    Given a test server route "/a" responding 200 with body "a"
    And a test server route "/b" responding 200 with body "b"
    And 2 test proxies
    And the fetch config override "http.proxy_strategy=sticky_host"
    When I fetch "/a"
    Then the fetch went through test proxy 1
    When I fetch "/b"
    Then the fetch went through test proxy 1
    And test proxy 2 served 0 requests

  Scenario: A retry leaves through a different proxy than the one that failed
    Given a test server route "/p" responding 200 with body "recovered"
    And 2 test proxies
    And test proxy 1 fails every request with 503
    And the fetch config override "advanced.retry_5xx=1"
    When I fetch "/p"
    Then the fetch status code is 200
    And the fetch body is "recovered"
    And the fetch went through test proxy 2
    And test proxy 1 served 1 requests
    And test proxy 2 served 1 requests

  Scenario: Wire bytes are metered per egress so a crawl's cost is known
    Given a test server route "/p" responding 200 with body "hello"
    And 1 test proxies
    When I fetch "/p"
    Then the crawl reports proxy traffic
