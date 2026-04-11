@frontend
Feature: Smoke & Navigation Journey
  End-to-end journey verifying app loads, sidebar, footer, settings,
  global tasks panel, channel chat, and git panel modes.

  Scenario: App load through sidebar, settings, global tasks, chat, and git panel
    Given I open the app in a browser
    Then I wait for text "Settings" to appear
    And the page should contain text "Tasks"
    And the page should contain text "Workflows"
    And the page should contain text "Containers"
    And the page should contain text "README"
    And I wait for text "dm" to appear

    # Navigate to Settings
    When I click on "Settings" in the sidebar
    Then I wait for text "Settings" to appear

    # Open and close global tasks panel
    When I open the global tasks panel
    And I click on the button with title "Close panel"
    Then I wait for text "TASKS (" to disappear

    # Set up channel, verify chat and modes
    Given I set up a test channel via API for directory "/tmp/bdd-mega-smoke"
    And I open the app in a browser
    And I wait for text "bdd-mega-smoke" to appear
    And I click on "bdd-mega-smoke" in the sidebar
    Then I wait for "textarea" to be visible
    And the page should contain text "Agent"
    And the page should contain text "Plan"

    # Git panel tabs
    And I wait for text "UNCOMMITTED DIFF" to appear
    And the page should contain text "BRANCHES DIFF"
    And the page should contain text "COMMITS"
    When I click on the button with text "BRANCHES DIFF"
    Then the page should contain text "BRANCHES DIFF"
    When I click on the button with text "COMMITS"
    Then the page should contain text "COMMITS"

    # Sessions panel: open, verify empty state, refresh, new session button
    When I add a "Sessions" panel
    Then I wait for text "No sessions found" to appear
    And the page should contain text "Select a session to resume"
    When I click on the button with title "Refresh sessions"
    Then the page should contain text "No sessions found"
    When I click on the button with text "+ New"
    Then I wait for text "Select a session to resume" to disappear
