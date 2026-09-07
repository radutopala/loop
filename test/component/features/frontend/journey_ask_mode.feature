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

  Scenario: A /loop command leaves a still-pending ask card up
    # The composer used to dismiss the ask/plan cards after every send, so a
    # /loop command (which never touches the park) hid the question while the
    # backend kept blocking the channel — no card left to answer, no drain.
    When I inject an ask_user event with question "Which database?" and options "postgres,sqlite"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    # Trailing space so the /loop command dropdown hides and Enter sends
    # instead of accepting a dropdown entry.
    And I type "/loop status " into "textarea"
    And I press Enter
    Then the page should contain text "CLAUDE HAS QUESTIONS"
    And the page should contain text "Which database?"

  Scenario: Typing an answer resolves the ask and the backend event drops the card
    # Typed text while parked routes through POST /ask/resolve; the card is
    # cleared only by the resulting agent.ask_resolved broadcast, so the FE can
    # never hide a question the backend still considers pending.
    When I inject an ask_user event with question "Which database?" and options "postgres,sqlite"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    And I type "postgres please" into "textarea"
    And I press Enter
    Then I wait for text "CLAUDE HAS QUESTIONS" to disappear

  Scenario: Stopping the run leaves a still-pending ask card up
    # Stop used to dismiss the ask/plan cards too, so hitting it while parked
    # hid the question without resolving the park — the channel stayed blocked
    # with nothing left for the user to answer.
    When I inject an ask_user event with question "Which database?" and options "postgres,sqlite"
    And I wait for text "CLAUDE HAS QUESTIONS" to appear
    And I inject an agent.status running event
    And I click on the button with title "Stop"
    Then the page should contain text "CLAUDE HAS QUESTIONS"
    And the page should contain text "Which database?"
