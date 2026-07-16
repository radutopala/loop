@frontend @slow @ask-mode
Feature: Ask user question card
  When the agent emits AskUserQuestion the chat shows an AskUserQuestionCard
  with one button per option and a Send button. Verifies the card renders
  against an injected event and that selecting an option enables Send.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-ask-fe"
    And I open the app in a browser
    And I wait for text "bdd-ask-fe" to appear
    And I click on "bdd-ask-fe" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: AskUserQuestionCard renders the question and option buttons
    When I inject an ask_user event with question "Which database?" and options "postgres,sqlite,redis"
    Then I wait for text "CLAUDE HAS QUESTIONS" to appear
    And the page should contain text "Which database?"
    And the page should contain text "postgres"
    And the page should contain text "sqlite"
    And the page should contain text "redis"
    And the page should contain text "Other..."
    And the page should contain text "Send Answers"

  Scenario: Selecting an option keeps the card open with Send Answers visible
    When I inject an ask_user event with question "Pick one" and options "yes,no"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    And I click on the button with text "yes"
    Then the page should contain text "Send Answers"

  Scenario: Send Answers posts to /ask/resolve and dismisses the card
    # Exercises the resolveAsk("answer") path on the FE — clicking Send Answers
    # hits POST /api/channels/{id}/ask/resolve which clears the park flag,
    # priority-bumps the answer into the queue, and the FE clears the card via
    # the onSent callback.
    When I inject an ask_user event with question "Pick one" and options "yes,no"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    And I click on the button with text "yes"
    And I click on the button with text "Send Answers"
    Then I wait for text "CLAUDE HAS QUESTIONS" to disappear

  Scenario: A running run does not wipe a still-pending ask card
    When I inject an ask_user event with question "Which database?" and options "postgres,sqlite"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    And I inject an agent.status running event
    Then the page should contain text "CLAUDE HAS QUESTIONS"
    And the page should contain text "Which database?"

  Scenario: Sidebar lights the ask pill while the channel is parked on AskUserQuestion
    # agent.ask_user → applyEvent sets state.askUserQuestions → refreshAskUserMembership
    # adds the channel ID to askUserChannelIdsRef → ChannelItem renders
    # <StatusPill label="ask" title="Agent is asking a question">. Clicking
    # Send Answers fires clearAskUser() → clearAskUserPill(channelId) → set
    # delete → pill disappears.
    When I inject an ask_user event with question "Pick one" and options "yes,no"
    Then I wait for text "CLAUDE HAS QUESTIONS" to appear
    And the element "[data-testid='sidebar'] [title='Agent is asking a question']" should be visible
    When I click on the button with text "yes"
    And I click on the button with text "Send Answers"
    Then I wait for text "CLAUDE HAS QUESTIONS" to disappear
    And the element "[data-testid='sidebar'] [title='Agent is asking a question']" should not exist
