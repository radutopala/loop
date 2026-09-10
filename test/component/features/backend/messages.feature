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
