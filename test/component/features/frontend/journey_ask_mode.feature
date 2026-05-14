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
