@frontend @learn
Feature: Learn from runs
  The composer's Learn switch turns a channel's learn pass on or off and
  sticks to the channel. A learn pass runs in a hidden learn thread and files
  proposals; the Learn badge in the chat pane's header says what it's doing
  and opens the Learn view: the chat and a Learn pane side by side over the
  layout, where each proposal is applied or dismissed.

  Background:
    Given I set up a test channel via API for git repo "bdd-learn"
    And I open the app in a browser
    And I wait for text "bdd-learn" to appear
    When I click on "bdd-learn" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: The Learn switch sticks to the channel across a reload
    # The test config sets no learn default, so the switch starts off, and a
    # channel that never learned has no badge.
    Then I wait for "[data-testid='learn-toggle'][data-on='false']" to be visible
    And the element "[data-testid='learn-badge']" should not exist
    When I click on "[data-testid='learn-toggle']"
    Then I wait for "[data-testid='learn-toggle'][data-on='true']" to be visible
    When I open the app in a browser
    And I click on "bdd-learn" in the sidebar
    Then I wait for "[data-testid='learn-toggle'][data-on='true']" to be visible
    When I send a GET request to "/api/channels/{channel_id}/learn"
    Then the response status should be 200
    And the response JSON "learn" should be "on"
    When I click on "[data-testid='learn-toggle']"
    Then I wait for "[data-testid='learn-toggle'][data-on='false']" to be visible

  Scenario: The Learn switch follows a change made in another window
    Then I wait for "[data-testid='learn-toggle'][data-on='false']" to be visible
    # Another window turning learn on sends channel.learn to every window.
    When I inject a "channel.learn" event for the channel with data:
      """
      {"learn":"on"}
      """
    Then I wait for "[data-testid='learn-toggle'][data-on='true']" to be visible

  Scenario: Applying a ticket proposal links the channel's ticket
    Given the current channel has a learn thread
    And the element "[data-testid='header-ticket']" should not exist
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"ticket_url","title":"Link the ticket","rationale":"The user pasted it.","payload":{"ticket_url":"https://example.atlassian.net/browse/PROJ-7"}}
      ]}
      """
    Then the response status should be 201
    When I tag the chat's composer
    And I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "https://example.atlassian.net/browse/PROJ-7" to appear
    And the page should contain text "ticket"
    When I click on "[data-testid='learn-apply']"
    Then I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    And I wait for "[data-testid='header-ticket']" to be visible
    And the element "[data-testid='header-ticket']" should contain text "PROJ-7"
    # The chat header has no layout controls: the badge closes the view.
    And the element "[data-testid='learn-split-chat'] [title='Close pane']" should not exist
    And the element "[data-testid='learn-split-chat'] [title='Add panel']" should not exist
    When I click on "[data-testid='learn-split'] [data-testid='learn-badge']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "[data-learn-chat-leaf] textarea" to be visible
    # The chat went back to its pane, still the same chat.
    And the composer in "[data-learn-chat-leaf]" is the one I tagged

  Scenario: Proposals filed by a learn pass are dismissed and applied from the Learn view
    # The proposals go through the real API a learn pass files them with;
    # its learn.proposals event lights the badge in the open window.
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","rationale":"The run set up the learn journey.","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    And I wait for text "2 proposals" to appear
    # The badge sits at the right of the chat pane's header.
    And the element "[id^='pane-header-slot-'] [data-testid='learn-badge']" should fit inside the window
    # The chat and the Learn pane open side by side over the layout; nothing
    # holding them may scroll meanwhile. The chat moves over from its pane
    # without being mounted again.
    When I tag the chat's composer
    And I open the Learn view and nothing behind it scrolls
    Then the Learn view shows the chat and the Learn pane side by side
    And the composer in "[data-testid='learn-split-chat']" is the one I tagged
    And the element "[data-testid='learn-split']" should fit inside the window
    And the element "[data-testid='learn-split-logo']" should be visible
    # The chat keeps its pane header, with the badge that closes the view.
    And the element "[data-testid='learn-split-chat'] #pane-header-slot-learn-split [data-testid='learn-badge']" should be visible
    # The chat pane was emptied, so the chat isn't mounted twice.
    And the element "[data-learn-chat-leaf] textarea" should not exist
    And I wait for text "Describe the thread" to appear
    And the page should contain text "Name the thread after its work"
    And the page should contain text "→ bdd-learn-renamed"
    # Newest first: the description card leads, so its Dismiss is the first.
    When I click on "[data-testid='learn-dismiss']"
    Then I wait for "[data-testid='learn-proposal'][data-status='dismissed']" to be visible
    And I wait for text "1 proposal" to appear
    # Only the rename is still open; applying it renames the channel.
    When I click on "[data-testid='learn-apply']"
    Then I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    And I wait up to "5s" for "[data-testid='learn-apply']" to disappear
    And I click on "bdd-learn-renamed" in the sidebar
    # After a reload the settled proposals are still listed, and the badge
    # stays (dim) since the channel now has a learn thread to look back at.
    When I open the app in a browser
    And I click on "bdd-learn-renamed" in the sidebar
    Then I wait for "[data-testid='learn-badge']" to be visible
    And the element "[data-testid='learn-badge']" should contain text "learn"
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    And the element "[data-testid='learn-proposal'][data-status='dismissed']" should be visible
    When I click on "[data-testid='learn-close']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "[data-learn-chat-leaf] textarea" to be visible

  Scenario: The chat stays where it was scrolled as the Learn view opens and closes
    Given the current channel has a learn thread
    When I inject a user message with content "please do the thing"
    And I inject 40 bot messages with content "a bot reply long enough to wrap over several lines in the chat pane, and to wrap over a different number of lines once the pane is wider or narrower, so that the chat is many screens tall and its height changes with its width"
    And I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[{"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}]}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    # A chat pane wider than the view's half: in the Learn view its messages
    # wrap differently, and the chat must still show the same place.
    When I drag the divider after the chat pane by 150px
    And I scroll the chat to the bottom and note where it is
    And I click on "[data-testid='learn-badge']" watching the chat's scroll
    Then the Learn view shows the chat and the Learn pane side by side
    And the chat is still scrolled where I noted
    When I click on "[data-testid='learn-split'] [data-testid='learn-badge']" watching the chat's scroll
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And the chat is still scrolled where I noted
    # And from the middle, where the chat doesn't pin itself back.
    When I scroll the chat halfway and note where it is
    And I click on "[data-testid='learn-badge']" watching the chat's scroll
    Then the Learn view shows the chat and the Learn pane side by side
    And the chat is still scrolled where I noted
    When I click on "[data-testid='learn-split'] [data-testid='learn-badge']" watching the chat's scroll
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And the chat is still scrolled where I noted

  Scenario: The badge shows a running learn pass until its thread's run ends
    Given the current channel has a learn thread
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    Then I wait for "[data-testid='learn-badge'][data-running='true']" to be visible
    And the element "[data-testid='learn-badge']" should contain text "learning…"
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "reviewing the last run…" to appear
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run"}
      """
    Then I wait for "[data-testid='learn-badge'][data-running='false']" to be visible
    And I wait for text "proposals from the last runs" to appear
    And the page should not contain text "learning…"
    # The Learn view's own chat header has the badge too; it closes the view.
    When I click on "[data-testid='learn-split'] [data-testid='learn-badge']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "[id^='pane-header-slot-'] [data-testid='learn-badge']" to be visible
