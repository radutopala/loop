@frontend @slow
Feature: Prompt Shortcuts Journey
  End-to-end journey verifying prompt shortcuts appear in the chat input
  and the # picker works with keyboard navigation.

  Scenario: Shortcut picker shows shortcuts and sends prompt
    # Pre-seed shortcuts via API
    Given I add a prompt shortcut "bdd-test-lint" with prompt "Run make lint and report results" via API
    And I add a prompt shortcut "bdd-other" with prompt "Something else" via API

    # Set up channel and open browser
    Given I set up a test channel via API for directory "/tmp/bdd-shortcuts"
    And I open the app in a browser
    And I wait for text "bdd-shortcuts" to appear
    When I click on "bdd-shortcuts" in the sidebar
    Then I wait for "textarea" to be visible

    # Verify the # button appears (shortcuts are loaded)
    And the element "button[title='Prompt shortcuts']" should be visible

    # Click the # button to open the shortcut picker
    When I click on the button with title "Prompt shortcuts"
    Then I wait for text "#bdd-test-lint" to appear
    And the page should contain text "bdd-test-lint"

    # The button types the # itself, so typing after it filters the picker
    And the field "textarea" should hold "#"
    And the page should contain text "#bdd-other"
    When I type "bdd-test" into "textarea"
    Then the field "textarea" should hold "#bdd-test"
    And I wait for text "#bdd-other" to disappear
    And the page should contain text "#bdd-test-lint"

    # Close by pressing Escape
    When I press Escape
    Then I wait for text "#bdd-test-lint" to disappear
    And the field "textarea" should hold "#bdd-test"

    # Closing without typing takes the button's # back out
    When I clear the "textarea" field
    And I click on the button with title "Prompt shortcuts"
    Then I wait for text "#bdd-test-lint" to appear
    When I press Escape
    Then I wait for text "#bdd-test-lint" to disappear
    And the field "textarea" should hold ""

    # Type # into textarea to trigger the picker via keyboard
    When I type "#" into "textarea"
    Then I wait for text "#bdd-test-lint" to appear

    # Press Escape to dismiss the picker
    When I press Escape
    Then I wait for text "#bdd-test-lint" to disappear
