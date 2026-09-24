@frontend @pagination
Feature: Chat Pagination Journey
  The chat loads a page of recent messages and pages in older ones as the
  reader scrolls up. An older page goes in above what's being read, so the
  view stays on the same message instead of jumping to the new page's start.

  Scenario: Scrolling up pages in older messages without moving the view
    Given I set up a test channel via API for git repo "bdd-chat-pagination"
    And I open the app in a browser
    And I wait for text "bdd-chat-pagination" to appear
    And I serve a chat history of 150 messages from the timeline

    When I click on "bdd-chat-pagination" in the sidebar
    And I wait for "textarea" to be visible
    Then I wait for text "history message 149" to appear

    When I scroll the chat to the top and note the first message
    Then an older page loads above the noted message without moving it
