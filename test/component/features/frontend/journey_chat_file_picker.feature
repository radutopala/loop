@frontend @slow
Feature: Chat @ File Picker Journey
  End-to-end journey verifying the chat input @ trigger opens a unified
  picker that surfaces both the @LoopBot mention and workspace files.

  Scenario: @ shows LoopBot mention together with workspace files
    Given I set up a test channel via API for git repo "bdd-file-picker"
    And I open the app in a browser
    And I wait for text "bdd-file-picker" to appear
    When I click on "bdd-file-picker" in the sidebar
    Then I wait for "textarea" to be visible

    # Type @ to open the merged picker
    When I type "@" into "textarea"
    Then I wait for text "@LoopBot" to appear
    And I wait for text "@README.md" to appear

    # Escape dismisses the picker
    When I press Escape
    Then I wait for text "@README.md" to disappear

  Scenario: Partial @ filters workspace files
    Given I set up a test channel via API for git repo "bdd-file-picker-partial"
    And I open the app in a browser
    And I wait for text "bdd-file-picker-partial" to appear
    When I click on "bdd-file-picker-partial" in the sidebar
    Then I wait for "textarea" to be visible

    # Type @REA — fuzzy/prefix matches README.md
    When I type "@REA" into "textarea"
    Then I wait for text "@README.md" to appear

    When I press Escape
    Then I wait for text "@README.md" to disappear
