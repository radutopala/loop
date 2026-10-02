@frontend @fork
Feature: Fork the conversation at a message
  Hovering a chat message shows +fork. It forks the conversation at that
  message into a new thread and opens it: at an agent reply the thread
  continues from the reply, and at a user message from just before it,
  with the message back in the composer. A message whose place in the
  session can't be found turns the button red with the reason.

  Background:
    Given I set up a test channel via API for git repo "bdd-fork"
    And the current channel has a finished turn "plan the release" replying "Here is the plan." in session "bdd-fork-sess"
    And I open the app in a browser
    And I wait for text "bdd-fork" to appear
    When I click on "bdd-fork" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: +fork on a reply opens a new thread that continues from it
    When I hover over the element with text "Here is the plan."
    And I click on "[data-msg-uuid]:not([data-is-user]) [data-testid='fork-message-btn']"
    Then I wait for text "(fork)" to appear
    # The new thread is open: the source's turn isn't in it (its transcript
    # isn't on disk to import) and its composer is empty.
    And I wait for text "Here is the plan." to disappear
    And the field "textarea" should hold ""

  Scenario: +fork on a prompt whose transcript is gone says why it failed
    When I hover over the element with text "plan the release"
    And I click on "[data-msg-uuid][data-is-user='true'] [data-testid='fork-message-btn']"
    Then I wait for "[data-testid='fork-message-btn'][data-state='error']" to be visible
    And the element "[data-testid='fork-message-btn'][data-state='error']" should contain text "Fork failed"
    And the page should not contain text "(fork)"
