@backend
Feature: Channel management
  Channels represent project directories that Loop manages.

  Scenario: Create a channel for a directory
    When I send a POST request to "/api/channels" with body:
      """
      {"dir_path": "/tmp/bdd-test-channel", "platform": "local"}
      """
    Then the response status should be 200
    And the response JSON "channel_id" should not be empty

  Scenario: Delete a channel
    Given a channel exists for directory "/tmp/bdd-delete-test"
    When I send a DELETE request to "/api/channels/{channel_id}"
    Then the response status should be 204
