@frontend @background-tasks
Feature: Background tasks in the chat
  While a run waits on background tasks, the chat keeps them on a line of
  their own, which other activity doesn't replace, until they finish or the
  run ends.

  Scenario: Background tasks stay in view until they finish
    Given I set up a test channel via API for git repo "bdd-background-tasks"
    And I open the app in a browser
    And I wait for text "bdd-background-tasks" to appear

    When I click on "bdd-background-tasks" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a user message with content "watch CI in the background"
    And I wait for text "watch CI in the background" to appear
    And I inject an agent.status running event

    # A task starts: its line shows, and later activity doesn't replace it.
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": "1 background task: watch CI"}
      """
    Then I wait for "[data-testid='background-tasks']" to be visible
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "thinking", "description": "120"}
      """
    Then I wait for text "Thinking… (120 tokens)" to appear
    And the element "[data-testid='background-tasks']" should contain text "Waiting on 1 background task: watch CI"

    # None are left: the line goes.
    When I inject a "agent.activity" event for the channel with data:
      """
      {"activity": "background_tasks", "description": ""}
      """
    Then I wait up to "5s" for "[data-testid='background-tasks']" to disappear
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
      {"activity": "background_tasks", "description": "1 background task: watch CI"}
      """
    Then I wait for "[data-testid='background-tasks']" to be visible

    When I inject an agent.status completed event
    Then I wait up to "5s" for "[data-testid='background-tasks']" to disappear
