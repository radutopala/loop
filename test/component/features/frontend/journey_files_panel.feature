@frontend @slow
Feature: Files Panel Journey
  End-to-end journey verifying the standalone Files panel renders the
  workspace tree, exposes its toolbar, and lists workspace files.

  Scenario: Files panel can be added to a channel and lists workspace files
    Given I set up a test channel via API for git repo "bdd-files-panel"
    And I open the app in a browser
    And I wait for text "bdd-files-panel" to appear
    When I click on "bdd-files-panel" in the sidebar
    Then I wait for "textarea" to be visible

    # Add the standalone Files panel
    When I add a "Files" panel
    Then the element "[data-testid='file-tree-panel']" should be visible
    And I wait for text "WORKSPACE" to appear

    # Refresh and new-file buttons are exposed by the Files panel toolbar
    And the element "button[title='Refresh files']" should be visible
    And the element "button[title='New file']" should be visible

    # The workspace root is auto-expanded; the seeded README.md is visible
    Then I wait for text "README.md" to appear

    # Refreshing the tree keeps README.md visible
    When I click on the button with title "Refresh files"
    Then the page should contain text "README.md"
