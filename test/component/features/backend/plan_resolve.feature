@backend
Feature: Plan resolve endpoint
  When the agent emits ExitPlanMode, the channel is parked until the user
  picks an action via POST /api/channels/{id}/plan/resolve. The endpoint
  validates the request body before touching the queue.

  Scenario: Reject a plan clears the pause without inserting a message
    Given a channel exists for directory "/tmp/bdd-plan-reject"
    When I send a POST request to "/api/channels/{channel_id}/plan/resolve" with body:
      """
      {"action":"reject"}
      """
    Then the response status should be 204

  Scenario: Unknown action is rejected with 400
    Given a channel exists for directory "/tmp/bdd-plan-bad-action"
    When I send a POST request to "/api/channels/{channel_id}/plan/resolve" with body:
      """
      {"action":"nope"}
      """
    Then the response status should be 400

  Scenario: Deny without a prompt is rejected with 400
    Given a channel exists for directory "/tmp/bdd-plan-deny-noprompt"
    When I send a POST request to "/api/channels/{channel_id}/plan/resolve" with body:
      """
      {"action":"deny"}
      """
    Then the response status should be 400

  Scenario: Malformed JSON is rejected with 400
    Given a channel exists for directory "/tmp/bdd-plan-bad-json"
    When I send a POST request to "/api/channels/{channel_id}/plan/resolve" with body:
      """
      not json
      """
    Then the response status should be 400
