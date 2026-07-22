@frontend @slow @plan-mode
Feature: Plan mode review card
  When the agent emits ExitPlanMode the chat shows an ExitPlanCard with
  three actions (Approve & Execute / Request Changes / Discard). Verifies
  the card renders against an injected event, exercises the local-only
  Request Changes textarea toggle (open + cancel), and the Discard path.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-plan-fe"
    And I open the app in a browser
    And I wait for text "bdd-plan-fe" to appear
    And I click on "bdd-plan-fe" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: ExitPlanCard renders with all three actions when agent emits exit_plan
    When I inject an exit_plan event with plan "Step 1: refactor handler; Step 2: add tests; Step 3: document"
    Then I wait for text "PLAN READY FOR REVIEW" to appear
    And the page should contain text "Step 1: refactor handler"
    And the page should contain text "Approve & Execute"
    And the page should contain text "Request Changes"
    And the page should contain text "Discard"

  Scenario: Request Changes opens a textarea with Send/Cancel, Cancel closes it
    When I inject an exit_plan event with plan "Refactor the handler"
    And I wait for text "PLAN READY FOR REVIEW" to appear
    And I click on the button with text "Request Changes"
    Then I wait for text "Send changes" to appear
    And the page should contain text "Cancel"
    When I click on the button with text "Cancel"
    Then I wait for text "Send changes" to disappear

  Scenario: Discard dismisses the card without sending a prompt
    When I inject an exit_plan event with plan "Reject me"
    And I wait for text "PLAN READY FOR REVIEW" to appear
    And I click on the button with text "Discard"
    Then I wait for text "PLAN READY FOR REVIEW" to disappear

  Scenario: A running run does not wipe a still-pending plan card
    When I inject an exit_plan event with plan "Survive the running event"
    And I wait for text "PLAN READY FOR REVIEW" to appear
    And I inject an agent.status running event
    Then the page should contain text "PLAN READY FOR REVIEW"
    And the page should contain text "Survive the running event"

  Scenario: Sidebar lights the plan pill while the channel is parked on ExitPlanMode
    # agent.exit_plan → applyEvent sets state.exitPlanRequest → refreshPlanMembership
    # adds the channel ID to planChannelIdsRef → ChannelItem renders
    # <StatusPill label="plan" title="Plan awaiting approval">. Discarding the
    # card fires clearExitPlan() → clearPlanPill(channelId) → set delete →
    # pill disappears.
    When I inject an exit_plan event with plan "Pill lifecycle plan"
    Then I wait for text "PLAN READY FOR REVIEW" to appear
    And the element "[data-testid='sidebar'] [title='Plan awaiting approval']" should be visible
    When I click on the button with text "Discard"
    Then I wait for text "PLAN READY FOR REVIEW" to disappear
    And the element "[data-testid='sidebar'] [title='Plan awaiting approval']" should not exist
