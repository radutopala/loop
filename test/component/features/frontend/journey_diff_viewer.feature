@frontend
Feature: Diff Viewer Journey
  File navigation, expand/collapse, and focus indicator in the diff viewer.

  Scenario: Diff viewer file expand selects file and collapse all works
    Given I set up a test channel via API for git repo "bdd-diff-viewer"
    And I create uncommitted files "alpha.txt, beta.txt" in the repo
    And I open the app in a browser
    And I wait for text "bdd-diff-viewer" to appear

    # Navigate to channel — diff panel shows uncommitted changes
    When I click on "bdd-diff-viewer" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for text "UNCOMMITTED DIFF" to appear
    Then I wait for text "alpha.txt" to appear
    And the page should contain text "beta.txt"
    And the page should contain text "1 / 2"

    # Click on beta.txt to expand — should also select it (nav bar updates to 2 / 2)
    When I click on the button with text "beta.txt"
    Then I wait for text "2 / 2" to appear

    # Expand all files then collapse all
    When I click on the button with title "Expand all"
    And I click on the button with title "Collapse all"
    Then the page should contain text "alpha.txt"
    And the page should contain text "beta.txt"

  Scenario: Status badges differentiate staged, unstaged, and untracked files
    Given I set up a test channel via API for git repo "bdd-diff-status"
    And I stage a new file "added.txt" in the repo
    And I modify "README.md" without staging
    And I create uncommitted files "fresh.txt" in the repo
    And I open the app in a browser
    And I wait for text "bdd-diff-status" to appear

    When I click on "bdd-diff-status" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for text "UNCOMMITTED DIFF" to appear
    Then I wait for text "added.txt" to appear
    And the page should contain text "README.md"
    And the page should contain text "fresh.txt"
    And the page should contain text "staged"
    And the page should contain text "unstaged"
    And the page should contain text "new"

  Scenario: Find bar counts content matches and jumps to a file by path
    Given I set up a test channel via API for git repo "bdd-diff-search"
    And I modify "README.md" without staging
    And I create uncommitted files "src/alpha.txt" in the repo
    And I open the app in a browser
    And I wait for text "bdd-diff-search" to appear

    When I click on "bdd-diff-search" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for text "UNCOMMITTED DIFF" to appear
    Then I wait for text "README.md" to appear

    # Content mode: the rewritten README line is the only hunk line with the term.
    When I click on "[data-testid='diff-search-toggle']"
    And I type "unstaged README" into "[data-testid='diff-search-input']"
    Then the element "[data-testid='diff-search-count']" should contain text "1 / 1"

    # A leading ">" turns the same box into a ranked file jump.
    When I clear and type ">alpha" into "[data-testid='diff-search-input']"
    Then I wait for "[data-testid='diff-search-path-result']" to be visible
    And the element "[data-testid='diff-search-path-result']" should contain text "src/alpha.txt"

    # Enter opens the chosen file and dismisses the bar.
    When I press Enter
    Then the element "[data-testid='diff-search-input']" should not exist
