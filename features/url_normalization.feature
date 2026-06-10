Feature: URL normalization and resolution
  Discovered URLs are resolved against their source page and normalized
  the way search engine crawlers do: fragments stripped (unless configured),
  percent-encoding canonicalized, default ports dropped, relative paths resolved.

  Scenario Outline: Resolving links found on a page
    Given a page at "<base>"
    When a link with href "<href>" is discovered
    Then the resolved URL is "<resolved>"

    Examples:
      | base                          | href                        | resolved                                  |
      | https://ex.com/a/b            | c                           | https://ex.com/a/c                        |
      | https://ex.com/a/b/           | c                           | https://ex.com/a/b/c                      |
      | https://ex.com/a/b            | /c                          | https://ex.com/c                          |
      | https://ex.com/a/b            | //cdn.ex.com/x.js           | https://cdn.ex.com/x.js                   |
      | http://ex.com/a/b             | //cdn.ex.com/x.js           | http://cdn.ex.com/x.js                    |
      | https://ex.com/a/b            | https://other.com/p?q=1     | https://other.com/p?q=1                   |
      | https://ex.com/a/b            | ../up                       | https://ex.com/up                         |
      | https://ex.com/a/             | ./same                      | https://ex.com/a/same                     |
      | https://ex.com/a              | ?q=2                        | https://ex.com/a?q=2                      |

  Scenario Outline: Normalization rules
    When the URL "<input>" is normalized
    Then the result is "<output>"

    Examples:
      | input                                  | output                               |
      | https://ex.com:443/a                   | https://ex.com/a                     |
      | http://ex.com:80/a                     | http://ex.com/a                      |
      | http://ex.com:8080/a                   | http://ex.com:8080/a                 |
      | https://EX.com/Path                    | https://ex.com/Path                  |
      | https://ex.com                         | https://ex.com/                      |
      | https://ex.com/a#section               | https://ex.com/a                     |
      | https://ex.com/a b                     | https://ex.com/a%20b                 |
      | https://ex.com/café               | https://ex.com/caf%C3%A9             |

  Scenario: Fragments are kept when crawl_fragments is enabled
    Given crawl_fragments is enabled
    When the URL "https://ex.com/a#section" is normalized
    Then the result is "https://ex.com/a#section"

  Scenario Outline: Invalid URLs are recognized
    When the URL "<input>" is checked for validity
    Then it is reported as "<verdict>"

    Examples:
      | input                  | verdict |
      | https://ex.com/ok      | valid   |
      | hppts://ex.com/        | invalid |
      | javascript:void(0)     | invalid |
      | mailto:x@ex.com        | invalid |
      | tel:+123456            | invalid |
      | https://               | invalid |

  Scenario Outline: Scope classification against start URL
    Given the crawl started at "https://www.ex.com/blog/"
    When the URL "<url>" is classified
    Then its scope is "<scope>"

    Examples:
      | url                              | scope    |
      | https://www.ex.com/blog/post-1   | internal |
      | https://www.ex.com/about         | internal |
      | https://shop.ex.com/item         | external |
      | https://other.com/x              | external |

  Scenario: crawl_all_subdomains makes sibling subdomains internal
    Given the crawl started at "https://www.ex.com/" with crawl_all_subdomains enabled
    When the URL "https://shop.ex.com/item" is classified
    Then its scope is "internal"

  Scenario: CDN domains are classified internal
    Given the crawl started at "https://www.ex.com/" with CDN "assets.cdn.net"
    When the URL "https://assets.cdn.net/img.png" is classified
    Then its scope is "internal"

  Scenario Outline: Folder depth computation
    When the folder depth of "<url>" is computed
    Then the depth is <depth>

    Examples:
      | url                                | depth |
      | https://ex.com/                    | 0     |
      | https://ex.com/page                | 0     |
      | https://ex.com/a/                  | 1     |
      | https://ex.com/a/page              | 1     |
      | https://ex.com/a/b/c/              | 3     |

  Scenario Outline: Path type classification of an href
    When a link with href "<href>" is examined
    Then its path type is "<type>"

    Examples:
      | href                    | type              |
      | https://ex.com/a        | absolute          |
      | //cdn.ex.com/a          | protocol_relative |
      | /a/b                    | root_relative     |
      | a/b                     | path_relative     |
      | ../a                    | path_relative     |
