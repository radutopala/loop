@frontend @slow
Feature: Workspace root selector
  Host/Docker shell panes and the Git panel expose a workspace-root selector in
  the pane header when the channel has more than one root (primary dir +
  extra_dirs). With a single root the selector is hidden.

  Scenario: Shell and git pane headers show the root selector for multi-root channels
    Given I set up a test channel via API for git repo "bdd-shell-roots"
    And I add an extra workspace root via API
    And I open the app in a browser
    And I wait for text "bdd-shell-roots" to appear
    When I click on "bdd-shell-roots" in the sidebar
    Then I wait for "textarea" to be visible

    # Host shell pane: the root selector sits in the pane header. It only renders
    # when the channel has more than one root, so its presence proves multi-root.
    When I add a "Host Shell" panel
    Then the element "[data-testid='terminal-root-select']" should be visible

    # Git pane: the same selector, in its header, governing every tab.
    When I add a "Git" panel
    Then the element "[data-testid='git-panel-root-select']" should be visible

    # The selector opens a menu listing each root by name with its path; picking
    # one shows its path on the button.
    When I click on "[data-testid='git-panel-root-select']"
    Then the element "[data-testid='context-menu']" should contain text "bdd-extra-root-"
    When I click on "bdd-extra-root-" in the context menu
    Then the element "[data-testid='context-menu']" should not exist
    And the element "[data-testid='git-panel-root-select']" should contain text "bdd-extra-root-"

  Scenario: Root selector is hidden for single-root channels
    Given I set up a test channel via API for git repo "bdd-single-root"
    And I open the app in a browser
    And I wait for text "bdd-single-root" to appear
    When I click on "bdd-single-root" in the sidebar
    Then I wait for "textarea" to be visible

    When I add a "Git" panel
    Then the element "[data-testid='git-panel']" should be visible
    And the element "[data-testid='git-panel-root-select']" should not exist
