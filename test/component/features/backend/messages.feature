@backend
Feature: Queued message deletion and steering
  Users can cancel a waiting message from the queue before the agent picks
  it up, or steer it to the front of the queue. The DELETE endpoint rejects
  malformed requests and is_processed=1 rows, and broadcasts message.deleted
  when a waiting row is removed. The steer endpoint promotes a waiting row and
  cancels the active run so the row is claimed next; rows that cannot be
  promoted (unknown, already claimed, or in another channel) return 404 and
  leave the run alone.

  Scenario: DELETE rejects a request without channel_id
    Given a channel exists for directory "/tmp/bdd-del-msg-400"
    When I send a DELETE request to "/api/messages/any-msg-id"
    Then the response status should be 400

  Scenario: DELETE returns 404 for an unknown msg_id
    Given a channel exists for directory "/tmp/bdd-del-msg-404"
    When I send a DELETE request to "/api/messages/does-not-exist?channel_id={channel_id}"
    Then the response status should be 404

  Scenario: DELETE returns 404 when channel_id does not match
    Given a channel exists for directory "/tmp/bdd-del-msg-mismatch"
    When I send a DELETE request to "/api/messages/some-id?channel_id=wrong-channel"
    Then the response status should be 404

  Scenario: Steer returns 404 for an unknown msg_id
    Given a channel exists for directory "/tmp/bdd-steer-msg-404"
    When I send a POST request to "/api/channels/{channel_id}/queued/does-not-exist/steer" with body:
      """
      {}
      """
    Then the response status should be 404

  Scenario: Steer returns 404 when the channel does not match
    Given a channel exists for directory "/tmp/bdd-steer-chan-mismatch"
    When I send a POST request to "/api/channels/wrong-channel/queued/some-id/steer" with body:
      """
      {}
      """
    Then the response status should be 404

  @add-to-turn
  Scenario: A message added with no turn running is queued
    Given a channel exists for directory "/tmp/bdd-add-no-turn"
    When I connect to the events WebSocket for the channel
    And I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"{channel_id}","content":"add me to the turn","delivery":"add"}
      """
    Then the response status should be 204
    When I wait up to "5s" for a WebSocket event of type "message.created"
    Then the WebSocket event data "content" should be "add me to the turn"
    And the WebSocket event data "is_processed" should be "false"

  @message-origin
  Scenario: A message from another channel names its sender in its tags
    Given a channel exists for directory "/tmp/bdd-message-origin"
    When I connect to the events WebSocket for the channel
    And I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"{channel_id}","content":"from elsewhere","from_channel_id":"sender-channel","delay_seconds":3600}
      """
    Then the response status should be 200
    When I wait up to "5s" for a WebSocket event of type "message.created"
    Then the WebSocket event data "content" should be "from elsewhere"
    And the WebSocket event data "tags" should be "map[origin_channel:sender-channel]"

  @message-origin
  Scenario: A message to a channel that doesn't exist fails
    When I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"no-such-channel","content":"anyone there?"}
      """
    Then the response status should be 404

  @message-origin
  Scenario: A message from its own channel has no tags
    Given a channel exists for directory "/tmp/bdd-message-no-origin"
    When I connect to the events WebSocket for the channel
    And I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"{channel_id}","content":"from here","from_channel_id":"{channel_id}","delay_seconds":3600}
      """
    Then the response status should be 200
    When I wait up to "5s" for a WebSocket event of type "message.created"
    Then the WebSocket event data "tags" should be "<nil>"
