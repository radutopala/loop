@frontend @needs-you
Feature: Sessions that need you
  A bell beside the sidebar's tabs narrows both Recent and Tree to the rows
  with a lit pill (an approval, a question, a plan, a review, a config to
  trust), keeping the channels that lead to them. Right-clicking it picks
  which pills show and count.

  Scenario: The bell keeps only the rows waiting on you
    Given I set up a test channel via API for directory "/tmp/bdd-needs-you"
    And I create a thread "bdd-ny-quiet" under the current channel via API
    And I create a thread "bdd-ny-waiting" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-ny-quiet" to appear
    And the element "[data-testid='sidebar-needs-you'][aria-pressed='false']" should be visible

    When I inject a gate.approval_requested event for the last created thread with req_id "bdd-ny-gate" and target "/tmp/bdd-ny.txt"
    Then I wait up to "5s" for "[data-testid='sidebar'] [data-pill='gate']" to be visible

    # The waiting thread stays, with its channel; the quiet one goes.
    When I click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='sidebar-needs-you'][aria-pressed='true']" should be visible
    And the element "[data-testid='sidebar']" should contain text "bdd-ny-waiting"
    And the element "[data-testid='sidebar']" should contain text "bdd-needs-you"
    And the element "[data-testid='sidebar']" should not contain text "bdd-ny-quiet"

    # Turning it off brings everything back.
    When I click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='sidebar-needs-you'][aria-pressed='false']" should be visible
    And the element "[data-testid='sidebar']" should contain text "bdd-ny-quiet"

  Scenario: A pill turned off in the bell's menu neither shows nor needs you
    Given I set up a test channel via API for directory "/tmp/bdd-needs-you-pick"
    And I create a thread "bdd-nyp-waiting" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-nyp-waiting" to appear
    When I inject a gate.approval_requested event for the last created thread with req_id "bdd-nyp-gate" and target "/tmp/bdd-nyp.txt"
    Then I wait up to "5s" for "[data-testid='sidebar'] [data-pill='gate']" to be visible

    # Turning gate off hides its pill and leaves nothing waiting on you.
    When I right-click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='context-menu']" should contain text "an approval"
    When I click on "gate" in the context menu
    Then the element "[data-testid='sidebar'] [data-pill='gate']" should not exist
    When I click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='sidebar']" should not contain text "bdd-nyp-waiting"

    # Turning it back on brings the pill and the row back.
    When I right-click on "[data-testid='sidebar-needs-you']"
    And I click on "gate" in the context menu
    Then I wait up to "5s" for "[data-testid='sidebar'] [data-pill='gate']" to be visible
    And the element "[data-testid='sidebar']" should contain text "bdd-nyp-waiting"

    # The choice is in the global config, so a fresh page still has it.
    When I right-click on "[data-testid='sidebar-needs-you']"
    And I click on "cmt" in the context menu
    And I open the app in a browser
    And I wait up to "10s" for "[data-testid='sidebar-needs-you']" to be visible
    And I right-click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='context-menu'] [aria-checked='false']" should contain text "cmt"
    When I click on "cmt" in the context menu
    And I right-click on "[data-testid='sidebar-needs-you']"
    Then the element "[data-testid='context-menu'] [aria-checked='false']" should not exist

  Scenario: The bar's controls say what they do and how they're set
    Given I set up a test channel via API for directory "/tmp/bdd-needs-you-info"
    And I open the app in a browser
    And I wait for "[data-testid='sidebar-needs-you']" to be visible

    # The bell lists every pill with what it waits on, marking the ones off.
    When I rest the pointer on the element "[data-testid='sidebar-needs-you']"
    Then I wait for "[data-testid='sidebar-needs-you-info']" to be visible
    And the element "[data-testid='sidebar-needs-you-info-title']" should contain text "Showing all sessions"
    And the element "[data-testid='sidebar-needs-you-info-gate']" should contain text "an approval"
    And the element "[data-testid='sidebar-needs-you-info-comment']" should contain text "review comments"
    When I move the pointer off the sidebar
    Then the element "[data-testid='sidebar-needs-you-info']" should not exist

    # The task toggle says how each tab is set.
    When I rest the pointer on the element "[data-testid='sidebar-hide-tasks']"
    Then I wait for "[data-testid='sidebar-hide-tasks-info']" to be visible
    And the element "[data-testid='sidebar-hide-tasks-info-tree']" should contain text "shown"
    When I click on "[data-testid='sidebar-hide-tasks']"
    And I move the pointer off the sidebar
    And I rest the pointer on the element "[data-testid='sidebar-hide-tasks']"
    Then I wait for "[data-testid='sidebar-hide-tasks-info']" to be visible
    And the element "[data-testid='sidebar-hide-tasks-info-tree']" should contain text "hidden"
    When I click on "[data-testid='sidebar-hide-tasks']"
    And I move the pointer off the sidebar

    # A tab says what it lists and how the filters are set. A run makes the
    # session recent, which brings the tabs.
    When I inject an agent.status running event
    Then I wait for "[data-testid='sidebar-tab-recent']" to be visible
    When I rest the pointer on the element "[data-testid='sidebar-tab-recent']"
    Then I wait for "[data-testid='sidebar-tab-recent-info']" to be visible
    And the element "[data-testid='sidebar-tab-recent-info']" should contain text "last 48 hours"
    And the element "[data-testid='sidebar-tab-recent-info-sessions']" should contain text "1"
    And the element "[data-testid='sidebar-tab-recent-info-showing']" should contain text "all sessions"
    When I rest the pointer on the element "[data-testid='sidebar-tab-tree']"
    Then I wait for "[data-testid='sidebar-tab-tree-info']" to be visible
    And the element "[data-testid='sidebar-tab-tree-info']" should contain text "Every channel"
    And the element "[data-testid='sidebar-tab-tree-info-task-threads']" should contain text "shown"
