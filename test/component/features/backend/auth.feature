@backend @auth
Feature: API authentication
  Every API caller proves who it is: the owner with the token the daemon
  writes, agents with their own scoped tokens. URL-loaded content goes
  through short-lived signed links instead.

  Scenario: Requests without a valid token are refused
    When I send a GET request to "/api/health" without the API token
    Then the response status should be 200
    When I send a GET request to "/api/channels" without the API token
    Then the response status should be 401
    When I send a GET request to "/api/channels" with the API token "not-the-token"
    Then the response status should be 401
    When I send a GET request to "/api/channels"
    Then the response status should be 200

  Scenario: WebSockets need the token too
    Then opening the WebSocket "/api/ws/terminal" without the API token should fail with status 401
    And opening the WebSocket "/api/ws/terminal" with the API token as a subprotocol should succeed

  Scenario: Content links serve a channel's files without the token, and only inside it
    Given I set up a test channel via API for git repo "auth-caps"
    And I create a file "site/index.html" in the repo with:
      """
      <p>hello</p>
      """
    And I create a symlink "site/escape.txt" in the repo pointing to "/etc/hostname"
    When I send a GET request to "/api/channels/{channel_id}/raw/0/site/index.html" without the API token
    Then the response status should be 401
    When I send a POST request to "/api/content-caps" with body:
      """
      {"kind": "raw", "channel_id": "{channel_id}", "root": 0}
      """
    Then the response status should be 200
    When I fetch "site/index.html" under the returned content link without the API token
    Then the response status should be 200
    And the response should contain "<p>hello</p>"
    When I fetch "site/escape.txt" under the returned content link without the API token
    Then the response status should be 400
    When I send a GET request to "/c/forged/site/index.html" without the API token
    Then the response status should be 403
