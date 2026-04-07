@frontend @slow
Feature: Global Tasks Panel Journey
  End-to-end journey covering global tasks panel with multi-channel tasks,
  detail view, edit, toggle, schedule display, and delete.

  Scenario: Global tasks from seeded tasks through edit, toggle, cron, and delete
    # Seed channel A with interval task
    Given I set up a test channel via API for directory "/tmp/bdd-mega-global-a"
    And I set up a test task via API with type "interval" prompt "mega-global-alpha" and schedule "30m"
    # Seed channel B with cron task
    And I set up a test channel via API for directory "/tmp/bdd-mega-global-b"
    And I set up a test task via API with type "cron" prompt "mega-global-beta" and schedule "*/5 * * * *"
    And I open the app in a browser
    And I wait for text "Settings" to appear

    # Open global panel, verify both tasks and channel names
    When I open the global tasks panel
    Then I wait up to "45s" for text "mega-global-alpha" to appear
    And I wait for text "mega-global-beta" to appear
    And the page should contain text "bdd-mega-global-a"
    And the page should contain text "bdd-mega-global-b"

    # Select interval task, verify detail
    When I click on "mega-global-alpha" in the global tasks panel
    And I wait for text "Task #" to appear
    Then the page should contain text "RUN HISTORY"
    And the page should contain text "No runs yet"
    And the page should contain text "Run Now"
    And the page should contain text "30m"
    And the page should contain text "Next run:"

    # Edit task prompt
    When I click on the button with text "Edit"
    And I wait for text "Edit Task #" to appear
    And I clear and type "mega-global-edited" into "textarea[rows='5']"
    And I click on the button with text "Save"
    Then I wait for text "mega-global-edited" to appear

    # Toggle disable, verify no next run
    When I click on the button with text "Disable"
    And I wait up to "15s" for text "Enable" to appear
    Then the page should not contain text "Next run:"

    # Delete interval task
    When I click on the button with text "Delete"
    Then I wait for text "mega-global-edited" to disappear
    And the page should contain text "mega-global-beta"

    # Select cron task, verify schedule
    When I click on "mega-global-beta" in the global tasks panel
    Then I wait for text "*/5 * * * *" to appear
    And the page should contain text "Run Now"

    # Delete cron task
    When I click on the button with text "Delete"
    Then I wait for text "mega-global-beta" to disappear
