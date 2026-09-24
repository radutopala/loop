@frontend @messageids
Feature: Chat Message Ids And Links
  Each message in the chat shows its row id in the database next to its
  time, so a message can be found in the database. A message that arrives
  live carries the same id it has after a reload.

  A message has a link, loop://channel/<channel-id>/<message-id>. Following
  one opens the channel and scrolls the message into view, paging in older
  messages until it's loaded. The message is outlined, blinking twice, and
  the outline stays until the message has been in view for 5 seconds.

  Scenario: A message shows its stored id live and after a reload
    Given I set up a test channel via API for git repo "bdd-chat-message-ids"
    And I open the app in a browser
    And I wait for text "bdd-chat-message-ids" to appear

    When I click on "bdd-chat-message-ids" in the sidebar
    And I wait for "textarea" to be visible
    And I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {"template": "canvas", "title": "Live row", "js": "ctx.fillRect(0, 0, 1, 1)"}
      """
    Then the response status should be 201
    And I wait for "[data-testid='chat-component']" to be visible
    And the chat shows the stored id of each message

    When I open the app in a browser
    And I wait for text "bdd-chat-message-ids" to appear
    And I click on "bdd-chat-message-ids" in the sidebar
    Then I wait for "[data-testid='chat-component']" to be visible
    And the chat shows the stored id of each message

  Scenario: A link in the chat pages back to an older message and shows it
    Given I set up a test channel via API for git repo "bdd-chat-message-link"
    And I open the app in a browser
    And I wait for text "bdd-chat-message-link" to appear
    And I serve a chat history of 150 messages from the timeline

    When I click on "bdd-chat-message-link" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for text "history message 149" to appear

    # Message 20 is two pages back from the latest
    When I follow a link to message 20 posted in the chat
    Then the linked message should be highlighted in view
    And the linked message should blink twice

    # Time out of view doesn't count toward clearing the outline
    When I scroll the linked message out of view for "6s"
    And I scroll the linked message back into view
    Then the linked message should stay highlighted for "4s" and clear by "7s"

  Scenario: Opening the app at a message link shows the message
    Given I set up a test channel via API for git repo "bdd-chat-message-deep-link"
    And I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {"template": "canvas", "title": "Linked", "js": "ctx.fillRect(0, 0, 1, 1)"}
      """
    Then the response status should be 201

    When I open the app at a link to the latest message
    Then the linked message should be highlighted in view
