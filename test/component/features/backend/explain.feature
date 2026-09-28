@backend @explain
Feature: Explain a turn
  A channel's explain switch sticks to the channel, and each turn it ran can
  be explained: an explanation is written up by a read-only fork of the
  channel's session in a hidden explain thread, and returned as it is until
  it's explained again.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-explain"

  Scenario: The explain switch sticks to the channel
    When I send a GET request to "/api/channels/{channel_id}/explain"
    Then the response status should be 200
    And the response JSON "available" should be "true"
    And the response JSON "explain" should be ""
    And the response JSON "enabled" should be "false"
    When I send a PUT request to "/api/channels/{channel_id}/explain" with body:
      """
      {"explain":"on"}
      """
    Then the response status should be 204
    When I send a GET request to "/api/channels/{channel_id}/explain"
    Then the response JSON "explain" should be "on"
    And the response JSON "enabled" should be "true"
    When I send a PUT request to "/api/channels/{channel_id}/explain" with body:
      """
      {"explain":"maybe"}
      """
    Then the response status should be 400

  Scenario: Explaining a turn checks it, and returns its explanation until it's explained again
    When I send a GET request to "/api/channels/{channel_id}/explanations"
    Then the response status should be 200
    And the response should contain "[]"
    When I send a POST request to "/api/channels/{channel_id}/explanations" with body:
      """
      {}
      """
    Then the response status should be 400
    And the response should contain "message_id is required"
    When I send a POST request to "/api/channels/{channel_id}/explanations" with body:
      """
      {"message_id":"no-such-message"}
      """
    Then the response status should be 400
    And the response should contain "not a bot message"
    # The channel never ran an agent, so there's no session to fork.
    Given the current channel has a finished turn "add a lint target" replying "Added a lint target."
    When I send a POST request to "/api/channels/{channel_id}/explanations" with body:
      """
      {"message_id":"{explain_msg_id}"}
      """
    Then the response status should be 409
    And the response should contain "no session"
    # An explanation there is returned as it is, without a new run.
    Given the turn has an explanation:
      """
      ### Summary

      Added a `lint` target to the Makefile.
      """
    When I send a POST request to "/api/channels/{channel_id}/explanations" with body:
      """
      {"message_id":"{explain_msg_id}"}
      """
    Then the response status should be 200
    And the response JSON "status" should be "done"
    And the response JSON "explain_channel_id" should not be empty
    When I send a GET request to "/api/channels/{channel_id}/explanations"
    Then the response status should be 200
    And the response should contain "Added a `lint` target to the Makefile."
    And the response should contain "add a lint target"
    And the response should contain "Added a lint target."
    # Re-explaining forks the session again, which the channel still lacks.
    When I send a POST request to "/api/channels/{channel_id}/explanations" with body:
      """
      {"message_id":"{explain_msg_id}","force":true}
      """
    Then the response status should be 409
    # The explain thread is hidden: it isn't a channel of its own to the API.
    When I send a GET request to "/api/channels/{explain_channel_id}/explain"
    Then the response status should be 404
