@proxy_on_block
Feature: Use the proxy only when blocked (http.proxy_on_block)
  A crawl starts from this machine's IP. When the site starts rate-limiting or
  blocking it, bluesnake holds new fetches, lets the ones in flight finish,
  switches once to the configured proxy for the rest of the crawl (renders
  included) and re-fetches the URLs that were blocked. A block seen before the
  switch is held, never recorded: the audit must describe the site, not the
  firewall's answer to this IP.

  Scenario: A site that starts returning 429s is finished through the proxy
    Given a blocking site whose home page links 12 pages and answers direct requests with 429 after 3 pages
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    When I crawl the blocking site
    Then the crawl switched to the proxy
    And 13 pages were recorded, each exactly once
    And every recorded page has status 200
    And every page's recorded route is the route it really took
    And pages were recorded both before and after the switch
    And no request reached the site directly after the switch
    And the switch is stored with the crawl for resume

  Scenario: Isolated 403s are findings, not a block
    Given a blocking site whose home page links 5 pages and answers direct requests with 429 after 1000 pages
    And the blocking site's page "/m1" answers 403 on every route
    And the blocking site's page "/m2" answers 403 on every route
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    When I crawl the blocking site
    Then the crawl did not switch to the proxy
    And the blocking site's page "/m1" is recorded with status 403
    And the blocking site's page "/m1" was fetched directly 2 times
    And the blocking site's page "/m2" is recorded with status 403
    And the fallback proxy carried no page requests

  Scenario: A burst of blocks from an external host does not trip the switch
    Given a blocking site whose home page links 2 pages and answers direct requests with 429 after 1000 pages
    And an external site answering 999, linked 10 times from the blocking site's home page
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    And the blocking crawl config "links.external.crawl=true"
    When I crawl the blocking site
    Then the crawl did not switch to the proxy

  Scenario: Three firewall challenges in a row trip the switch
    Given a blocking site whose home page links 8 pages and answers direct requests with a Cloudflare challenge after 1 pages
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    And the blocking crawl config "speed.max_threads=1"
    When I crawl the blocking site
    Then the crawl switched to the proxy
    And every recorded page has status 200

  Scenario: Blocked attempts do not use the max_urls budget
    Given a blocking site whose home page links 30 pages and answers direct requests with 429 after 3 pages
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    And the blocking crawl config "limits.max_urls=10"
    When I crawl the blocking site
    Then 10 pages were recorded, each exactly once
    And every recorded page has status 200

  Scenario: A robots.txt blocked before the switch is fetched again and obeyed
    Given a blocking site whose home page links 5 pages and answers direct requests with 429 after 0 pages
    And the blocking site links "/private/x" from its home page
    And the blocking site's robots.txt answers 429 directly and disallows "/private/" through the proxy
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    When I crawl the blocking site
    Then the crawl switched to the proxy
    And the blocking site's page "/private/x" is blocked by robots.txt
    And the blocking site's page "/private/x" was never fetched through the proxy

  Scenario: Still blocked through the proxy: blocks are recorded and counted, the crawl completes
    Given a blocking site whose home page links 8 pages and answers direct requests with 429 after 1 pages
    And the blocking site also answers proxied page requests with 429
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    When I crawl the blocking site
    Then the crawl switched to the proxy
    And the crawl counted responses still blocked through the proxy
    And every page's recorded route is the route it really took

  Scenario: A wrong proxy password stops the crawl before anything is fetched
    Given a blocking site whose home page links 2 pages and answers direct requests with 429 after 1000 pages
    And a fallback proxy requiring username "u" and password "right"
    And proxy_on_block is configured with the fallback proxy using username "u" and password "wrong"
    When I crawl the blocking site
    Then the blocking crawl fails with "407"
    And the blocking site received no requests

  Scenario: A crawl that already switched resumes on the proxy
    Given a blocking site whose home page links 4 pages and answers direct requests with 429 after 0 pages
    And a fallback proxy
    And proxy_on_block is configured with the fallback proxy
    When I crawl the blocking site as a resume of a crawl that already switched
    Then the crawl switched to the proxy
    And the blocking site received no direct requests

  Scenario Outline: The toggle refuses configurations where it cannot work
    Given a config file with contents:
      """
      <yaml>
      """
    When the config is loaded
    Then loading fails with an error containing "proxy_on_block"

    Examples:
      | yaml                                                                                               |
      | http: {proxy_on_block: true}                                                                       |
      | http: {proxy_on_block: true, proxy: "http://p:1", proxy_include_direct: true}                      |
      | {http: {proxy_on_block: true, proxy: "http://p:1"}, advanced: {cookie_storage: persistent}}        |

  @chrome
  Scenario: Renders follow the switch to the proxy
    Given a blocking site whose home page links 6 pages and answers direct requests with 429 after 3 pages
    And a fallback proxy requiring username "u" and password "p"
    And proxy_on_block is configured with the fallback proxy using username "u" and password "p"
    And the blocking crawl config "rendering.mode=javascript"
    When I crawl the blocking site
    Then the crawl switched to the proxy
    And every page recorded through the proxy was rendered from the same page
    And no request reached the site directly after the switch
