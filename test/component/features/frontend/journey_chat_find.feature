@frontend @chatfind
Feature: Find In Chat
  The magnifier at the top right of the chat, or Cmd/Ctrl+F, opens a find
  bar. It searches all of the channel's messages on the server, not only the
  loaded pages, and counts the matches, newest first. Enter steps to an older
  match and Shift+Enter to a newer one, wrapping at both ends. Each match is
  outlined the way a message link shows its message, paging in older
  messages as needed, and the view goes to the term inside it, which is
  marked, so a long message doesn't show only its middle. Esc closes the bar.

  Scenario: Stepping through matches pages back to an older message
    Given I set up a test channel via API for git repo "bdd-chat-find"
    And I open the app in a browser
    And I wait for text "bdd-chat-find" to appear
    And I serve a chat history of 150 messages from the timeline

    When I click on "bdd-chat-find" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for text "history message 149" to appear

    # "message 14" matches message 14 and 140-149; the newest comes first.
    When I click on "[data-testid='chat-find-toggle']"
    And I type "Message 14" into "[data-testid='chat-find-input']"
    Then the element "[data-testid='chat-find-count']" should contain text "1 / 11"
    And message 150 should be highlighted in view

    When I press Enter
    Then the element "[data-testid='chat-find-count']" should contain text "2 / 11"
    And message 149 should be highlighted in view

    When I press Shift+Enter
    Then the element "[data-testid='chat-find-count']" should contain text "1 / 11"

    # Past the newest match it wraps to the oldest, pages back from the latest.
    When I press Shift+Enter
    Then the element "[data-testid='chat-find-count']" should contain text "11 / 11"
    And message 15 should be highlighted in view

    When I clear and type "nothing like this" into "[data-testid='chat-find-input']"
    Then the element "[data-testid='chat-find-count']" should contain text "no matches"

    When I press Escape
    Then the element "[data-testid='chat-find-bar']" should not exist
    And I wait for "[data-testid='chat-find-toggle']" to be visible
