@frontend @slow
Feature: Editor VCS Change Markers Journey
  The code editor shows git change markers in the gutter and a right-side
  overview ruler for uncommitted changes versus git HEAD.

  Scenario: Modified file shows a gutter change bar and the overview ruler
    Given I set up a test channel via API for git repo "bdd-editor-gutter"
    And I modify "README.md" without staging
    And I open the app in a browser
    And I wait for text "bdd-editor-gutter" to appear

    When I click on "bdd-editor-gutter" in the sidebar
    And I wait for "textarea" to be visible

    # Open the Editor layout tab and load the modified file from the tree
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "README.md" in the editor tree
    And I wait for text "unstaged README.md" to appear

    # The modified line carries a gutter change bar and the overview ruler appears
    Then I wait for ".cm-gitChange-modified" to be visible
    And the element "[data-testid='git-overview-ruler']" should be visible

  Scenario: New untracked file shows added-line gutter bars
    Given I set up a test channel via API for git repo "bdd-editor-gutter-new"
    And I create uncommitted files "feature.txt" in the repo
    And I open the app in a browser
    And I wait for text "bdd-editor-gutter-new" to appear

    When I click on "bdd-editor-gutter-new" in the sidebar
    And I wait for "textarea" to be visible

    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "feature.txt" in the editor tree
    And I wait for text "// feature.txt" to appear

    # Every line of a brand-new file is an addition (green gutter bar)
    Then I wait for ".cm-gitChange-added" to be visible
    And the element "[data-testid='git-overview-ruler']" should be visible
