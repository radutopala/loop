@backend
Feature: Scheduled task lifecycle
  Tasks are scheduled prompts that run on a cron or interval.

  Scenario: Create, list, and delete a task
    Given a channel exists for directory "/tmp/bdd-task-test"
    When I create a task with prompt "BDD test task" and schedule "5m"
    Then the task list should contain the created task
    When I send a DELETE request to "/api/tasks/{task_id}"
    Then the response status should be 204
