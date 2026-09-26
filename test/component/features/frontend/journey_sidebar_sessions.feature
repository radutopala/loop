@frontend @sidebar-sessions
Feature: Recent sessions in the sidebar
  The sidebar's Recent tab lists sessions from
  anywhere in it that had activity in the last 48 hours, newest first, with how
  long ago. A session waiting on you or with an agent running counts as
  active now, so it tops the list. Tabs switch between Recent and Tree
  (the channel tree), Tree until one is picked; with nothing recent only
  the tree shows, without tabs. Each tab has its own task-thread filter.

  Scenario: A run keeps a session at the top of Recent
    Given I set up a test channel via API for git repo "bdd-sessions"
    And I open the app in a browser
    And I wait for text "bdd-sessions" to appear

    # Nothing is recent yet: just the tree, no tabs, but the task filter stays.
    Then the element "[data-testid='sidebar-tabs']" should not exist
    And the element "[data-testid='sidebar-hide-tasks']" should be visible

    # A run makes the session recent, which brings the tabs.
    When I inject an agent.status running event
    Then I wait for "[data-testid='sidebar-tab-recent']" to be visible
    When I click on "[data-testid='sidebar-tab-recent']"
    Then I wait for "[data-testid='sidebar-recent']" to be visible
    And the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"
    And the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row'] [data-testid='session-kind-channel']" should be visible

    # Waiting on an approval: it stays in Recent and shows the gate pill.
    When I inject a gate.approval_requested event with req_id "bdd-sessions-gate", source "chat", and target "/tmp/bdd-sessions.txt"
    Then I wait up to "5s" for "[data-testid='sidebar-recent'] [title='Approval needed']" to be visible
    When I inject a gate.approval_resolved event with req_id "bdd-sessions-gate"
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"

    # The run ends: it stays the newest in Recent, now with its age.
    When I inject an agent.status completed event
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"
    And the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row']" should contain text "now"

    # The Tree tab swaps Recent for the tree; the tabs stay.
    When I click on "[data-testid='sidebar-tab-tree']"
    Then the element "[data-testid='sidebar-recent']" should not exist
    And the element "[data-testid='sidebar-tab-recent']" should be visible
    When I click on "[data-testid='sidebar-tab-recent']"
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"

    # The task filter is per tab: hiding tasks in Recent leaves Tree's alone.
    When I click on "[data-testid='sidebar-hide-tasks']"
    Then the element "[data-testid='sidebar-hide-tasks'][aria-pressed='true']" should be visible
    When I click on "[data-testid='sidebar-tab-tree']"
    Then the element "[data-testid='sidebar-hide-tasks'][aria-pressed='false']" should be visible
    When I click on "[data-testid='sidebar-tab-recent']"
    Then the element "[data-testid='sidebar-hide-tasks'][aria-pressed='true']" should be visible

    # A session row opens its channel.
    When I click on "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row']"
    Then I wait for "textarea" to be visible

  Scenario: A worktree thread in Recent has the tree's worktree icon
    Given I set up a test channel via API for git repo "bdd-sessions-wt"
    And I set up a worktree "recent-wt" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "recent-wt" to appear
    Then the element "[data-testid='session-kind-worktree']" should be visible

    When I inject an agent.status running event for the worktree thread
    Then I wait for "[data-testid='sidebar-tab-recent']" to be visible
    When I click on "[data-testid='sidebar-tab-recent']"
    Then I wait for "[data-testid='sidebar-recent']" to be visible
    And the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row'] [data-testid='session-kind-worktree']" should be visible

  Scenario: A plain thread in Recent has a thread icon
    Given I set up a test channel via API for git repo "bdd-sessions-thread"
    And I create a thread "recent-plain" under the current channel via API
    And I open the app in a browser
    And I wait for text "recent-plain" to appear

    When I inject an agent.status running event for the last created thread
    Then I wait for "[data-testid='sidebar-tab-recent']" to be visible
    When I click on "[data-testid='sidebar-tab-recent']"
    Then I wait for "[data-testid='sidebar-recent']" to be visible
    And the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row'] [data-testid='session-kind-thread']" should be visible
    And the element "[data-testid='sidebar-tab-recent-count']" should contain text "1"
    # The tree marks threads by indent, not an icon.
    When I click on "[data-testid='sidebar-tab-tree']"
    Then the element "[data-testid='session-kind-thread']" should not exist
