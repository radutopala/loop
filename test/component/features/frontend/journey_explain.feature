@frontend @explain
Feature: Explain a turn
  Each finished turn ends with an Explain button, below its last bot
  message. It explains the turn when it hasn't been, and otherwise shows the
  turn's explanation in the Explain view: the Learn view's, with the chat
  and the Explain pane side by side (docked beside the chat's tile on a
  canvas). The pane lists the channel's explanations with a link back to
  each turn and a Re-explain button. The composer's Explain switch explains
  every turn and sticks to the channel.

  Background:
    Given I set up a test channel via API for git repo "bdd-explain"
    And the current channel has a finished turn "add a lint target" replying "Added a lint target."
    And I open the app in a browser
    And I wait for text "bdd-explain" to appear
    When I click on "bdd-explain" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: The Explain switch sticks to the channel and follows other windows
    # The test config sets no explain default, so the switch starts off.
    Then I wait for "[data-testid='explain-toggle'][data-on='false']" to be visible
    # The switch is an icon; resting on it says what it is and does.
    When I rest the pointer on the element "[data-testid='explain-toggle']"
    Then I wait for "[data-testid='hover-tip']" to be visible
    And the element "[data-testid='hover-tip']" should contain text "Explain is off"
    When I click on "[data-testid='explain-toggle']"
    Then I wait for "[data-testid='explain-toggle'][data-on='true']" to be visible
    When I open the app in a browser
    And I click on "bdd-explain" in the sidebar
    Then I wait for "[data-testid='explain-toggle'][data-on='true']" to be visible
    When I send a GET request to "/api/channels/{channel_id}/explain"
    Then the response status should be 200
    And the response JSON "explain" should be "on"
    # Another window turning it off sends channel.explain to every window.
    When I inject a "channel.explain" event for the channel with data:
      """
      {"explain":"off"}
      """
    Then I wait for "[data-testid='explain-toggle'][data-on='false']" to be visible

  Scenario: An explanation shows in the Explain view as it runs, and can be explained again
    Then I wait for "[data-testid='explain-turn'][data-state='explain']" to be visible
    And the element "[data-testid='explain-turn']" should contain text "Explain"
    # At the end of the turn, below its reply.
    And the element "[data-testid='explain-turn']" should be below "[data-msg-uuid]:not([data-is-user]) p"
    And the element "[data-testid='learn-split']" should not exist
    # No explanation yet: no Explain label in the chat's header.
    And the element "[data-testid='explain-badge']" should not exist
    # A queued explanation: the button follows it, and opens it in the pane.
    When I inject a "explain.updated" event for the channel with data:
      """
      {"id":424242,"channel_id":"{channel_id}","message_id":"{explain_msg_id}","explain_channel_id":"explain-bdd","status":"queued","content":"","created_at":"{now-5s}","updated_at":"{now-5s}","prompt":"add a lint target","reply":"Added a lint target."}
      """
    Then I wait for "[data-testid='explain-turn'][data-state='pending']" to be visible
    And the element "[data-testid='explain-turn']" should contain text "Explain queued…"
    # The label only opens the view: the state is the button's.
    And I wait for "[data-testid='explain-badge']" to be visible
    And the element "[data-testid='explain-badge']" should contain text "explain"
    When I click on "[data-testid='explain-turn']"
    Then I wait for "[data-testid='learn-split'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And the element "[data-testid='learn-split-logo']" should be visible
    And the element "[data-testid='learn-split-chat'] textarea" should be visible
    And the element "[data-testid='explain-pane']" should contain text "explaining a turn…"
    And I wait for "[data-testid='explanation'][data-status='queued'][data-highlighted='true']" to be visible
    And the element "[data-testid='explanation-prompt']" should contain text "add a lint target"
    And the element "[data-testid='explanation-reply']" should contain text "Added a lint target."
    # Running, then done: the snippets the later events lack are kept.
    When I inject a "explain.updated" event for the channel with data:
      """
      {"id":424242,"channel_id":"{channel_id}","message_id":"{explain_msg_id}","explain_channel_id":"explain-bdd","status":"running","content":"","created_at":"{now-5s}","updated_at":"{now-3s}"}
      """
    Then I wait for "[data-testid='explanation'][data-status='running']" to be visible
    And the element "[data-testid='explain-turn']" should contain text "Explaining…"
    And the page should contain text "A forked session is writing up this turn…"
    When I inject a "explain.updated" event for the channel with data:
      """
      {"id":424242,"channel_id":"{channel_id}","message_id":"{explain_msg_id}","explain_channel_id":"explain-bdd","status":"done","content":"### Summary\n\nAdded a `lint` target to the Makefile.","created_at":"{now-5s}","updated_at":"{now-1s}"}
      """
    Then I wait for "[data-testid='explanation'][data-status='done']" to be visible
    And the element "[data-testid='explanation-content']" should contain text "Added a lint target to the Makefile."
    And the element "[data-testid='explanation-prompt']" should contain text "add a lint target"
    And I wait for "[data-testid='explain-turn'][data-state='open']" to be visible
    And the element "[data-testid='explain-turn']" should contain text "Explained"
    # Re-explaining forks the channel's session, which it doesn't have yet:
    # the failed request shows under the card.
    When I click on "[data-testid='explanation-reexplain']"
    Then I wait for "[data-testid='explanation'] [data-testid='explain-request-error']" to be visible
    And the element "[data-testid='explain-request-error']" should contain text "no session"
    # The pane's ✕ closes the view; the chat stays.
    When I click on "[data-testid='explain-close']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "textarea" to be visible

  Scenario: A stored explanation is listed after a reload and links back to its turn
    Given the turn has an explanation:
      """
      ### Summary

      Added a `lint` target to the Makefile.
      """
    When I open the app in a browser
    And I click on "bdd-explain" in the sidebar
    Then I wait for "[data-testid='explain-turn'][data-state='open']" to be visible
    # An explained turn isn't explained again: the button opens the view on it.
    When I click on "[data-testid='explain-turn']"
    Then I wait for "[data-testid='learn-split'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And I wait for "[data-testid='explanation'][data-status='done'][data-highlighted='true']" to be visible
    And the element "[data-testid='explanation-content']" should contain text "Added a lint target to the Makefile."
    And the element "[data-testid='explanation-prompt']" should contain text "add a lint target"
    And the element "[data-testid='explanation-goto']" should be visible
    And the element "[data-testid='explain-pane']" should contain text "write-ups of the chat's turns"
    # Rendered by default; Source shows the original markdown.
    And the element "[data-testid='explanation-mode-preview'][aria-pressed='true']" should be visible
    And the element "[data-testid='explanation-source']" should not exist
    When I click on "[data-testid='explanation-mode-source']"
    Then I wait for "[data-testid='explanation-source']" to be visible
    And the element "[data-testid='explanation-source']" should contain text "Added a `lint` target"
    And the element "[data-testid='explanation-content']" should not exist
    When I click on "[data-testid='explanation-mode-preview']"
    Then I wait for "[data-testid='explanation-content']" to be visible
    And the element "[data-testid='explanation-content'] h3" should contain text "Summary"
    And the element "[data-testid='explanation-content'] code" should contain text "lint"
    # Escape closes it, like the Learn view.
    When I press Escape
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    # The Explain label in the chat's header opens it too, and in the view's
    # own chat header closes it.
    And I wait for "[data-testid='explain-badge']" to be visible
    And the element "[data-testid='explain-badge']" should contain text "explain"
    When I click on "[data-testid='explain-badge']"
    Then I wait for "[data-testid='learn-split'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And the element "[data-testid='learn-split-chat'] #pane-header-slot-learn-split [data-testid='explain-badge'][aria-expanded='true']" should be visible
    When I click on "[data-testid='learn-split'] [data-testid='explain-badge']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear

  Scenario: On a canvas, the Explain pane docks beside the chat's tile
    Given the turn has an explanation:
      """
      ### Summary

      Added a `lint` target to the Makefile.
      """
    When I open the app in a browser
    And I click on "bdd-explain" in the sidebar
    # A new canvas starts empty; its first tile is the chat.
    And I click on "[title='New layout']"
    And I click on "[data-testid='new-layout-canvas']"
    And I click on "[data-testid='empty-layout-add-chat']"
    Then I wait for "[data-canvas-tile] [data-testid='explain-turn'][data-state='open']" to be visible
    When I click on "[data-canvas-tile] [data-testid='explain-turn']"
    Then I wait for "[data-testid='canvas-learn-dock'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And the Learn dock sits right of the chat's tile, the same size
    And the element "[data-testid='learn-split']" should not exist
    And I wait for "[data-testid='explanation'][data-status='done']" to be visible
    When I click on "[data-testid='explain-close']"
    Then I wait up to "5s" for "[data-testid='canvas-learn-dock']" to disappear
    And the element "[data-canvas-tile] textarea" should be visible
    # The Explain label in the chat's tile header opens and closes the dock.
    When I click on "[data-canvas-tile] [data-testid='explain-badge']"
    Then I wait for "[data-testid='canvas-learn-dock'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And the element "[data-canvas-tile] [data-testid='explain-badge'][aria-expanded='true']" should be visible
    When I click on "[data-canvas-tile] [data-testid='explain-badge']"
    Then I wait up to "5s" for "[data-testid='canvas-learn-dock']" to disappear

  Scenario: A request that fails shows on the button, which tries again
    Then I wait for "[data-testid='explain-turn'][data-state='explain']" to be visible
    # The channel never ran an agent, so there's no session to fork.
    When I click on "[data-testid='explain-turn']"
    Then I wait for "[data-testid='explain-turn'][data-state='error']" to be visible
    And the element "[data-testid='explain-turn']" should contain text "Explain failed"
    And I wait for "[data-testid='learn-split'][data-view='explain'] [data-testid='explain-pane']" to be visible
    And the element "[data-testid='explain-request-error']" should contain text "no session"
    When I click on "[data-testid='explain-turn']"
    Then I wait for "[data-testid='explain-turn'][data-state='error']" to be visible
