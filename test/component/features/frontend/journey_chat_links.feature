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
  Scenario: A file named in a code span opens it in the editor
    Given I set up a test channel via API for git repo "bdd-code-span-links"
    And I create a file "docs/deep/codespan.md" in the repo with:
      """
      codespanlinktarget
      """
    And I open the app in a browser
    And I wait for text "bdd-code-span-links" to appear

    When I click on "bdd-code-span-links" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a bot message with:
      """
      Code reply: see `codespan.md:L1` and run `make test`.
      """
    Then I wait for text "Code reply" to appear
    # A bare file name in backticks is a link to the one file it names,
    # deeper down, and stays code.
    And I wait for "code a[href='#']" to be visible
    And the element "code a[href='#']" should contain text "codespan.md:L1"
    When I click on "code a[href='#']"
    Then I wait for text "codespanlinktarget" to appear

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

  @chat-file-links
  Scenario: A file link in a side chat's Learn view opens the file in the channel's editor
    Given I set up a test channel via API for git repo "bdd-side-learn-links"
    And I create a file "notes/learn.md" in the repo with:
      """
      sidelearnlinktarget
      """
    And I open the app in a browser
    And I wait for text "bdd-side-learn-links" to appear

    When I click on "bdd-side-learn-links" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Chat (Fresh session)" panel
    Then I wait for "[data-testid='side-chat'] textarea" to be visible
    Given the current channel becomes its side chat
    And the current channel has a learn thread
    And the learn thread has a bot message:
      """
      Learn reply: see notes/learn.md for what changed.
      """
    When I open the app in a browser
    And I wait for text "bdd-side-learn-links" to appear
    And I click on "bdd-side-learn-links" in the sidebar
    Then I wait for "[data-testid='side-chat'] textarea" to be visible
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    And I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-side-learn-run"}
      """
    And I click on "[data-testid='learn-badge'][aria-expanded='false']"
    Then I wait for "[data-testid='side-chat-split'][data-view='learn']" to be visible
    And I wait for "[data-testid='side-chat-split'] [data-learn-thread] a[href='#']" to be visible
    # The link closes the view and opens the file in the channel's editor.
    When I click on "[data-testid='side-chat-split'] [data-learn-thread] a[href='#']"
    Then I wait up to "5s" for "[data-testid='side-chat-split']" to disappear
    And I wait for text "sidelearnlinktarget" to appear
