@frontend @slow @json-folding
Feature: JSON Folding Journey
  JSON shown for editing, in Settings and in the code editor, has fold
  arrows in the gutter that collapse an object or array.

  Scenario: The Settings JSON view folds the config's objects
    Given I open the app in a browser
    And I wait for text "Settings" to appear

    When I open the settings panel and select "JSON"
    Then I wait for "[data-testid='config-json-editor'] .cm-foldGutter span[title='Fold line']" to be visible

    When I click on "[data-testid='config-json-editor'] .cm-foldGutter span[title='Fold line']"
    Then I wait for "[data-testid='config-json-editor'] .cm-foldPlaceholder" to be visible

  Scenario: A JSON file in the editor folds its objects
    Given I set up a test channel via API for git repo "bdd-json-fold"
    And I create a file "data.json" in the repo with:
      """
      {
        "name": "fold me",
        "nested": {
          "list": [1, 2, 3]
        }
      }
      """
    And I open the app in a browser
    And I wait for text "bdd-json-fold" to appear

    When I click on "bdd-json-fold" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "data.json" in the editor tree
    Then I wait for ".cm-foldGutter span[title='Fold line']" to be visible

    When I click on ".cm-foldGutter span[title='Fold line']"
    Then I wait for ".cm-foldPlaceholder" to be visible
