Feature: HTTP fetching
  The fetcher treats network behaviour as data: redirects are never followed
  transparently (they are recorded with their target and re-enter discovery),
  errors and timeouts become no-response results, HSTS is emulated client-side
  as synthetic 307s, and 5xx responses can be retried. User-agent, custom
  headers and basic auth come from config.

  Scenario: A successful fetch records status, body and timing
    Given a test server route "/ok" responding 200 with body "hello"
    When I fetch "/ok"
    Then the fetch status code is 200
    And the fetch body is "hello"
    And a response time was recorded

  Scenario: Redirects are data, not followed
    Given a test server route "/old" redirecting 301 to "/new"
    And a test server route "/new" responding 200 with body "target"
    When I fetch "/old"
    Then the fetch status code is 301
    And the redirect target ends with "/new"
    And the redirect type is "http"
    And the server received 0 requests to "/new"

  Scenario: Relative redirect locations are resolved against the request URL
    Given a test server route "/a/old" redirecting 302 to "next"
    When I fetch "/a/old"
    Then the redirect target ends with "/a/next"

  Scenario: 5xx responses are retried when configured
    Given a test server route "/flaky" failing 2 times with 503 then responding 200
    And the fetch config override "advanced.retry_5xx=3"
    When I fetch "/flaky"
    Then the fetch status code is 200
    And the server received 3 requests to "/flaky"

  Scenario: Without retries a 5xx is returned as-is
    Given a test server route "/flaky" failing 2 times with 503 then responding 200
    When I fetch "/flaky"
    Then the fetch status code is 503
    And the server received 1 requests to "/flaky"

  Scenario: Custom headers and user-agent are sent
    Given a test server route "/echo" responding 200 with body "ok"
    And the fetch config override "http.user_agent=mybot/9"
    And the fetch config override "http.headers={Accept-Language: de}"
    When I fetch "/echo"
    Then the server saw user-agent "mybot/9" on "/echo"
    And the server saw header "Accept-Language" with value "de" on "/echo"

  Scenario: Browser-like headers match Screaming Frog's defaults
    Given a test server route "/echo" responding 200 with body "ok"
    When I fetch "/echo"
    Then the server saw header "Accept" with value "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8" on "/echo"
    And the server saw header "Cache-Control" with value "no-cache" on "/echo"
    And the server saw header "Pragma" with value "no-cache" on "/echo"
    And the server saw header "Accept-Language" with value "" on "/echo"

  Scenario: Browser default headers can be turned off
    Given a test server route "/echo" responding 200 with body "ok"
    And the fetch config override "http.browser_headers=false"
    When I fetch "/echo"
    Then the server saw header "Accept" with value "" on "/echo"

  Scenario: Configured headers win over the browser defaults
    Given a test server route "/echo" responding 200 with body "ok"
    And the fetch config override "http.headers={Accept: application/json}"
    When I fetch "/echo"
    Then the server saw header "Accept" with value "application/json" on "/echo"

  Scenario: HTTP/2 is negotiated by default against an HTTP/2 server
    Given a TLS test server route "/v" responding 200 with body "ok" that supports HTTP/2
    When I fetch "/v" over https
    Then the fetch status code is 200
    And the negotiated HTTP version is "HTTP/2.0"

  Scenario: The HTTP version can be forced to 1.1
    Given a TLS test server route "/v" responding 200 with body "ok" that supports HTTP/2
    And the fetch config override "http.version=1.1"
    When I fetch "/v" over https
    Then the fetch status code is 200
    And the negotiated HTTP version is "HTTP/1.1"

  Scenario: Basic auth applies to matching URL prefixes
    Given a test server route "/secure" requiring basic auth "alice" "s3cret"
    And basic auth is configured for the server with username "alice" and password "s3cret"
    When I fetch "/secure"
    Then the fetch status code is 200

  Scenario: Requests without matching auth config are not authenticated
    Given a test server route "/secure" requiring basic auth "alice" "s3cret"
    When I fetch "/secure"
    Then the fetch status code is 401

  Scenario: Timeouts become no-response results
    Given a test server route "/slow" that sleeps 1500ms before responding 200
    And the fetch config override "advanced.response_timeout_sec=1"
    When I fetch "/slow"
    Then the fetch reports a network error
    And the fetch status code is 0

  Scenario: Oversized bodies are truncated and flagged
    Given a test server route "/big" responding 200 with a body of 8 KB
    And the fetch config override "limits.max_page_size_kb=4"
    When I fetch "/big"
    Then the fetch body is truncated to 4096 bytes

  Scenario: HSTS makes later http fetches synthetic 307s
    Given a TLS test server route "/a" responding 200 with body "ok" and HSTS header "max-age=600"
    When I fetch "/a" over https
    And I fetch "/a" over plain http on the same host
    Then the fetch status code is 307
    And the fetch status is "HSTS Policy"
    And the redirect type is "hsts"
    And the server received 1 requests to "/a"

  Scenario: HSTS emulation can be disabled
    Given a TLS test server route "/a" responding 200 with body "ok" and HSTS header "max-age=600"
    And the fetch config override "advanced.respect_hsts=false"
    When I fetch "/a" over https
    And I fetch "/a" over plain http on the same host
    # no synthetic 307: the request really goes over the wire, and the
    # TLS-only server answers plain HTTP with 400, proving the request was
    # not turned around locally
    Then the fetch status code is 400
    And the redirect type is ""
