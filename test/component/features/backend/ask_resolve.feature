@backend
Feature: Ask resolve endpoint
  When the agent emits AskUserQuestion, the channel is parked until the user
  picks an action via POST /api/channels/{id}/ask/resolve. The endpoint
  validates the request body before touching the queue.

  Scenario: Cancel an ask clears the pause and inserts the stock prompt
    Given a channel exists for directory "/tmp/bdd-ask-cancel"
    When I send a POST request to "/api/channels/{channel_id}/ask/resolve" with body:
      """
      {"action":"cancel"}
      """
    Then the response status should be 204

  Scenario: Unknown action is rejected with 400
    Given a channel exists for directory "/tmp/bdd-ask-bad-action"
    When I send a POST request to "/api/channels/{channel_id}/ask/resolve" with body:
      """
      {"action":"nope"}
      """
    Then the response status should be 400

  Scenario: Answer without text is rejected with 400
    Given a channel exists for directory "/tmp/bdd-ask-answer-empty"
    When I send a POST request to "/api/channels/{channel_id}/ask/resolve" with body:
      """
      {"action":"answer"}
      """
    Then the response status should be 400

  Scenario: Malformed JSON is rejected with 400
    Given a channel exists for directory "/tmp/bdd-ask-bad-json"
    When I send a POST request to "/api/channels/{channel_id}/ask/resolve" with body:
      """
      not json
      """
    Then the response status should be 400
