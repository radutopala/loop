@frontend @chat-links
Feature: Chat Links Journey
  A link in a chat message, written bare or as markdown, shows a copy button
  while hovered. The button copies the link's address, not its text.

  Scenario: Hovering a link in a reply offers to copy its address
    Given I set up a test channel via API for git repo "bdd-chat-links"
    And I open the app in a browser
    And I wait for text "bdd-chat-links" to appear

    When I click on "bdd-chat-links" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a bot message with:
      """
      Links reply: see https://example.com/bare/page and [the docs](https://example.com/docs).
      """
    Then I wait for text "Links reply" to appear
    And I record clipboard writes

    # A bare URL copies itself.
    When I hover over the element with text "https://example.com/bare/page"
    And I click on "a[href='https://example.com/bare/page'] + [data-testid='link-copy']"
    Then the clipboard should hold "https://example.com/bare/page"

    # A markdown link copies its address, not its text.
    When I hover over the element with text "the docs"
    And I click on "a[href='https://example.com/docs'] + [data-testid='link-copy']"
    Then the clipboard should hold "https://example.com/docs"
