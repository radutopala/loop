@backend @learn
Feature: Learn a turn on demand
  A learn pass reviews one turn, in a fork of the channel's session cut at
  that turn's reply. Passes asked for different turns queue in the channel's
  hidden learn thread and each runs in turn: a newer one doesn't replace one
  still waiting.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-learn-turns"

  Scenario: Learn passes over two turns both queue, and neither replaces the other
    Given the current channel has a finished turn "add a lint target" replying "Added a lint target." in session "bdd-session"
    And the current channel has a finished turn "run the linter in CI" replying "Ran the linter in CI." in session "bdd-session"
    When I send a POST request to "/api/channels/{channel_id}/learn/passes" with body:
      """
      {"message_id":"{turn_1_msg_id}"}
      """
    Then the response status should be 200
    And the response JSON "message_id" should be "{turn_1_msg_id}"
    And the response JSON "learn_channel_id" should not be empty
    When I send a POST request to "/api/channels/{channel_id}/learn/passes" with body:
      """
      {"message_id":"{turn_2_msg_id}"}
      """
    Then the response status should be 200
    And the response JSON "message_id" should be "{turn_2_msg_id}"
    And the current channel's learn passes review turns 1 and 2, and none is superseded
