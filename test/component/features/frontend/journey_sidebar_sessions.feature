@frontend @sidebar-sessions
Feature: Recent sessions in the sidebar
  Above the channel tree, the sidebar's Recent section lists sessions from
  anywhere in it that had activity in the last day, newest first, with how
  long ago. A session waiting on you or with an agent running counts as
  active now, so it tops the list. The tree follows under All. Each section
  collapses, and an empty one isn't shown.

  Scenario: A run keeps a session at the top of Recent
    Given I set up a test channel via API for git repo "bdd-sessions"
    And I open the app in a browser
    And I wait for text "bdd-sessions" to appear

    When I inject an agent.status running event
    Then I wait for "[data-testid='sidebar-recent']" to be visible
    And the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"
    And the element "[data-testid='sidebar-section-all']" should be visible

    # Waiting on an approval: it stays in Recent and shows the gate pill.
    When I inject a gate.approval_requested event with req_id "bdd-sessions-gate", source "chat", and target "/tmp/bdd-sessions.txt"
    Then the element "[data-testid='sidebar-recent']" should contain text "gate"
    When I inject a gate.approval_resolved event with req_id "bdd-sessions-gate"
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"

    # The run ends: it stays the newest in Recent, now with its age.
    When I inject an agent.status completed event
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"
    And the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row']" should contain text "now"

    # Collapsing Recent hides its rows but keeps its header.
    When I click on "[data-testid='sidebar-section-recent']"
    Then the element "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row']" should not exist
    And the element "[data-testid='sidebar-section-recent']" should be visible
    When I click on "[data-testid='sidebar-section-recent']"
    Then the element "[data-testid='sidebar-recent']" should contain text "bdd-sessions"

    # A session row opens its channel.
    When I click on "[data-testid='sidebar-recent'] [data-testid='sidebar-session-row']"
    Then I wait for "textarea" to be visible
