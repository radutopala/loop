@frontend @chat-links
Feature: Chat Links Journey
  A link in a chat message, written bare or as markdown, shows a copy button
  while hovered, and right-clicking it offers Copy link. Both copy the
  link's address, not its text.

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
    And I click on "a[href='https://example.com/bare/page'] + span [data-testid='link-copy']"
    Then the clipboard should hold "https://example.com/bare/page"

    # A markdown link copies its address, not its text.
    When I hover over the element with text "the docs"
    And I click on "a[href='https://example.com/docs'] + span [data-testid='link-copy']"
    Then the clipboard should hold "https://example.com/docs"

    # Right-clicking a link offers to copy its address too.
    When I right-click on the element with text "the docs"
    And I click on the button with text "Copy link"
    Then the clipboard should hold "https://example.com/docs"

  @chat-file-links
  Scenario: A markdown link to a file opens it in the editor
    Given I set up a test channel via API for git repo "bdd-chat-file-links"
    And I create a file "notes/linked.md" in the repo with:
      """
      markdownlinktarget
      """
    And I open the app in a browser
    And I wait for text "bdd-chat-file-links" to appear

    When I click on "bdd-chat-file-links" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a bot message with:
      """
      File reply: see [notes/linked.md:1]({repo_path}/notes/linked.md) and [the docs](https://example.com/docs).
      """
    Then I wait for text "File reply" to appear
    # It reads as its label and opens the file, not a browser window.
    And I wait for "a[href='#']" to be visible
    And the element "a[href='#']" should contain text "notes/linked.md:1"
    When I click on "a[href='#']"
    Then I wait for text "markdownlinktarget" to appear

  @chat-file-links
  Scenario: A file link in a side chat opens the file in the channel's editor
    Given I set up a test channel via API for git repo "bdd-side-file-links"
    And I create a file "notes/side.md" in the repo with:
      """
      sidechatlinktarget
      """
    And I open the app in a browser
    And I wait for text "bdd-side-file-links" to appear

    When I click on "bdd-side-file-links" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Chat (Fresh session)" panel
    Then I wait for "[data-testid='side-chat'] textarea" to be visible
    Given the current channel becomes its side chat
    When I inject a bot message with:
      """
      Side reply: see [notes/side.md:1]({repo_path}/notes/side.md).
      """
    Then I wait for "[data-testid='side-chat'] a[href='#']" to be visible
    # The side chat shares the channel's roots: the link opens the file in
    # the channel's editor.
    When I click on "[data-testid='side-chat'] a[href='#']"
    Then I wait for text "sidechatlinktarget" to appear
