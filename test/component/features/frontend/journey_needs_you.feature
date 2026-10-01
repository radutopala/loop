@frontend @needs-you
Feature: Sessions that need you
  A bell beside the sidebar's tabs narrows both Recent and Tree to the rows
  with a lit pill (an approval, a question, a plan, a review, a config to
  trust), keeping the channels that lead to them.

  Scenario: The bell keeps only the rows waiting on you
    Given I set up a test channel via API for directory "/tmp/bdd-needs-you"
    And I create a thread "bdd-ny-quiet" under the current channel via API
    And I create a thread "bdd-ny-waiting" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-ny-quiet" to appear
    And the element "[data-testid='sidebar-needs-you'][aria-pressed='false']" should be visible

    When I inject a gate.approval_requested event for the last created thread with req_id "bdd-ny-gate" and target "/tmp/bdd-ny.txt"
    Then I wait up to "5s" for "[data-testid='sidebar'] [title='Approval needed']" to be visible

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
