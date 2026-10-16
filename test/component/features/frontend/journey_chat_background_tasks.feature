@frontend @background-tasks
Feature: Background tasks above the composer
  While a run waits on background tasks, the chat shows a pill per task
  above the composer, with its kind, description and a ticking timer, which
  other activity doesn't replace, until they finish or the run ends. A live
  session's tasks can be stopped one by one.

  Scenario: Background tasks stay in view until they finish
    Given I set up a test channel via API for git repo "bdd-background-tasks"
    And I open the app in a browser
    And I wait for text "bdd-background-tasks" to appear

    When I click on "bdd-background-tasks" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a user message with content "watch CI in the background"
    And I wait for text "watch CI in the background" to appear
    And I inject an agent.status running event

    # A task starts: its pill shows, and later activity doesn't replace it.
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": "1 background task: watch CI", "tasks": [{"id": "b1", "type": "local_bash", "description": "watch CI", "started_at": "2026-01-02T03:04:05Z"}]}
      """
    Then I wait for "[data-testid='background-tasks-bar']" to be visible
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "thinking", "description": "120"}
      """
    Then I wait for text "Thinking… (120 tokens)" to appear
    And the element "[data-testid='background-task-row']" should contain text "Shell"
    And the element "[data-testid='background-task-row']" should contain text "watch CI"
    And the element "[data-testid='background-task-elapsed']" should contain text "h"
    # A one-shot run's tasks can't be stopped.
    And the element "[data-testid='background-task-stop']" should not exist

    # None are left: the pill goes.
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": ""}
      """
    Then I wait up to "5s" for "[data-testid='background-tasks-bar']" to disappear
    And the page should contain text "Thinking… (120 tokens)"

  Scenario: Background tasks go when the run ends
    Given I set up a test channel via API for git repo "bdd-background-tasks-end"
    And I open the app in a browser
    And I wait for text "bdd-background-tasks-end" to appear

    When I click on "bdd-background-tasks-end" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a user message with content "watch CI in the background"
    And I wait for text "watch CI in the background" to appear
    And I inject an agent.status running event
    And I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": "1 background task: watch CI", "tasks": [{"id": "b1", "type": "local_bash", "description": "watch CI", "started_at": "2026-01-02T03:04:05Z"}]}
      """
    Then I wait for "[data-testid='background-tasks-bar']" to be visible

    When I inject an agent.status completed event
    Then I wait up to "5s" for "[data-testid='background-tasks-bar']" to disappear

  Scenario: A live session's background tasks can be stopped
    Given I set up a test channel via API for git repo "bdd-background-tasks-live"
    And I open the app in a browser
    And I wait for text "bdd-background-tasks-live" to appear

    When I click on "bdd-background-tasks-live" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a "live.status" event for the channel with data:
      """
      {"running": true, "busy": false}
      """
    And I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": "1 background task: sleep", "tasks": [{"id": "b9", "type": "local_agent", "description": "sleep", "started_at": "2026-01-02T03:04:05Z"}]}
      """
    Then I wait for "[data-testid='background-task-stop']" to be visible
    And the element "[data-testid='background-task-row']" should contain text "Agent"
