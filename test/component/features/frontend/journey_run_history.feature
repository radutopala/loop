@frontend @slow
Feature: Task Run History Accumulation
  Verify run history persists across multiple API-triggered runs.

  Scenario: Multiple API-triggered runs accumulate in run history
    Given I set up a test channel via API for directory "/tmp/bdd-mega-run-hist"
    And I set up a test task via API with prompt "mega-run-hist-task" and schedule "1h"

    # First run via API
    And I trigger Run Now via API for the current task
    And I wait up to "30s" for the task to stop running via API

    # Second run via API
    And I trigger Run Now via API for the current task
    And I wait up to "30s" for the task to stop running via API

    # Open browser and verify accumulated history
    And I open the app in a browser
    And I wait for text "bdd-mega-run-hist" to appear
    When I click on "bdd-mega-run-hist" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    And I wait for text "mega-run-hist-task" to appear
    And I click on "mega-run-hist-task" in the tasks panel
    And I wait for text "RUN HISTORY" to appear
    Then the page should contain text "failed"
