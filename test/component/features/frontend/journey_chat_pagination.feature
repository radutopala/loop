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

  Scenario: Paging on after a run ends doesn't load the same messages again
    Given I set up a test channel via API for git repo "bdd-chat-pagination-run"
    And I open the app in a browser
    And I wait for text "bdd-chat-pagination-run" to appear
    And I serve a chat history of 250 messages from the timeline

    When I click on "bdd-chat-pagination-run" in the sidebar
    And I wait for "textarea" to be visible
    Then I wait for text "history message 249" to appear

    # Page back once, so an older page sits under the latest one
    When I scroll the chat to the top and note the first message
    Then an older page loads above the noted message without moving it

    # A run ending refetches the latest page; paging further back must go on
    # from the oldest loaded message, not from under the latest page
    When I inject an agent.status running event
    And I inject an agent.status completed event
    Then the chat fetches its latest messages again
    When I scroll the chat to the top and note the first message
    Then an older page loads above the noted message without moving it
    And no chat message should appear twice
