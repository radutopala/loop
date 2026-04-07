@frontend @slow
Feature: Task WebSocket Events
  Verify task lifecycle events are received via WebSocket.

  Scenario: WebSocket receives create, update, delete, and run_completed events
    Given I set up a test channel via API for directory "/tmp/bdd-mega-ws"
    And I connect to the events WebSocket
    And the WebSocket connection should be established

    # task.created event
    When I set up a test task via API with prompt "mega-ws-task" and schedule "1h"
    Then I wait up to "5s" for a WebSocket event of type "task.created"

    # task.updated event (toggle disable)
    When I send a PATCH request to "/api/tasks/{task_id}" with body:
      """
      {"enabled": false}
      """
    Then I wait up to "5s" for a WebSocket event of type "task.updated"

    # Re-enable for run_completed test
    When I send a PATCH request to "/api/tasks/{task_id}" with body:
      """
      {"enabled": true}
      """
    And I wait up to "5s" for a WebSocket event of type "task.updated"

    # task.run_completed event
    When I trigger Run Now via API for the current task
    Then I wait up to "30s" for a WebSocket event of type "task.run_completed"
    And the WebSocket event data "status" should be "failed"
    And the WebSocket event data "task_id" should not be empty

    # task.deleted event
    When I send a DELETE request to "/api/tasks/{task_id}"
    Then I wait up to "5s" for a WebSocket event of type "task.deleted"
    And I close the WebSocket connection
