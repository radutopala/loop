@frontend @learn
Feature: Learn from runs
  The composer's Learn switch turns a channel's learn pass on or off and
  sticks to the channel. A learn pass reviews a turn in a hidden learn thread
  and files proposals; the turn ends with a Learn button that says what its
  pass is doing, or found, and shows it in the Learn view: the chat and a
  Learn pane side by side over the layout, where each proposal is applied or
  dismissed. The Learn label in the chat pane's header just opens the view.

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

  Scenario: Proposals filed by a learn pass are dismissed and applied from the Learn view
    # The proposals go through the real API a learn pass files them with;
    # its learn.proposals event brings the badge to the open window.
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","rationale":"The run set up the learn journey.","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
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

  Scenario: The Learn pane shows a running learn pass until its thread's run ends
    Given the current channel has a learn thread
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    # The label only opens the view: it says nothing of the pass.
    Then I wait for "[data-testid='learn-badge']" to be visible
    And the element "[data-testid='learn-badge']" should contain text "learn"
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "reviewing the last run…" to appear
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run"}
      """
    Then I wait for text "proposals from the last runs" to appear
    # A reply the user asked the learn thread for isn't a learn pass.
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"running","run_id":"bdd-learn-reply","trigger":"learn-reply"}
      """
    And I inject a "agent.status" event for the channel with data:
      """
      {"status":"completed","run_id":"bdd-nothing"}
      """
    Then the page should contain text "proposals from the last runs"
    And the page should not contain text "reviewing the last run…"
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"running","run_id":"bdd-learn-run-2","trigger":"learn"}
      """
    Then I wait for text "reviewing the last run…" to appear
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run-2"}
      """
    Then I wait for text "proposals from the last runs" to appear

  Scenario: Escape and a layout tab switch close the Learn view, and focus stays in the chat
    Given the current channel has a learn thread
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    And I click on "[data-learn-chat-leaf] textarea"
    And I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    # The learn thread's composer doesn't take the focus from the chat's.
    And the focus is in "[data-testid='learn-split-chat'] textarea"
    When I press Escape
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And the focus is in "[data-learn-chat-leaf] textarea"
    # Closing with focus in the Learn pane gives it back to the chat.
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I click on "[data-testid='learn-pane'] textarea"
    And I click on "[data-testid='learn-close']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And the focus is in "[data-learn-chat-leaf] textarea"
    # Switching to a layout tab without a chat pane closes the view; the
    # chat is back in its pane on the way back.
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I click on "[data-testid='layout-tab-Kanban']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    When I click on "[data-testid='layout-tab-Chat']"
    Then I wait for "[data-learn-chat-leaf] textarea" to be visible

  Scenario: The Learn view shows a failed request under its card, and a stuck apply comes back
    Given the current channel has a learn thread
    # A proposal the server doesn't know: applying it fails as a request.
    When I inject a "learn.proposals" event for the channel with data:
      """
      {"proposals":[
        {"id":999999,"channel_id":"{channel_id}","learn_channel_id":"{learn_channel_id}","kind":"description","title":"Unknown to the server","rationale":"","payload":"{\"description\":\"x\"}","status":"pending","created_at":"{now-5s}","updated_at":"{now-5s}"},
        {"id":999998,"channel_id":"{channel_id}","learn_channel_id":"{learn_channel_id}","kind":"description","title":"Stuck applying","rationale":"","payload":"{\"description\":\"y\"}","status":"applying","created_at":"{now-58s}","updated_at":"{now-58s}"}
      ]}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I click on "[data-testid='learn-apply']"
    Then I wait for "[data-testid='learn-request-error']" to be visible
    # The stuck one gets Retry and Dismiss back once it's a minute old,
    # without anything else rendering the pane again.
    And I wait up to "10s" for text "Retry" to appear

  Scenario: A learn run, a pass or a reply, marks nothing unread
    Given the current channel has a learn thread
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"running","run_id":"bdd-learn-reply","trigger":"learn-reply"}
      """
    And I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-reply","trigger":"learn-reply"}
      """
    And I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run","trigger":"learn"}
      """
    # Events are handled in order: once this one shows, those have been.
    And I inject a "learn.proposals" event for the channel with data:
      """
      {"proposals":[
        {"id":999999,"channel_id":"{channel_id}","learn_channel_id":"{learn_channel_id}","kind":"description","title":"Describe it","rationale":"","payload":"{\"description\":\"x\"}","status":"pending","created_at":"{now-5s}","updated_at":"{now-5s}"}
      ]}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    And the element "[title='Mark all as read']" should not exist
    # Any other run there would.
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-other-run","trigger":"chat"}
      """
    Then I wait for "[title='Mark all as read']" to be visible

  Scenario: Apply all applies every pending proposal, and a ticket proposal links the channel's ticket
    Given the current channel has a learn thread
    And the element "[data-testid='header-ticket']" should not exist
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"ticket_url","title":"Link the ticket","rationale":"The user pasted it.","payload":{"ticket_url":"https://example.atlassian.net/browse/PROJ-8"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    When I tag the chat's composer
    And I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "https://example.atlassian.net/browse/PROJ-8" to appear
    And the element "[data-testid='learn-dismiss-all']" should be visible
    When I click on "[data-testid='learn-apply-all']"
    Then I wait up to "5s" for "[data-testid='learn-proposal'][data-status='pending']" to disappear
    And the element "[data-testid='learn-proposal'][data-status='applied']" should be visible
    And I wait for "[data-testid='header-ticket']" to be visible
    And the element "[data-testid='header-ticket']" should contain text "PROJ-8"
    # Nothing's left open, so neither bulk button shows.
    And the element "[data-testid='learn-apply-all']" should not exist
    And the element "[data-testid='learn-dismiss-all']" should not exist
    And the element "[data-testid='learn-badge']" should contain text "learn"
    # The chat header has no layout controls: the badge closes the view.
    And the element "[data-testid='learn-split-chat'] [title='Close pane']" should not exist
    And the element "[data-testid='learn-split-chat'] [title='Add panel']" should not exist
    When I click on "[data-testid='learn-split'] [data-testid='learn-badge']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "[data-learn-chat-leaf] textarea" to be visible
    # The chat went back to its pane, still the same chat.
    And the composer in "[data-learn-chat-leaf]" is the one I tagged

  Scenario: Dismiss all dismisses every open proposal, and a failed request shows under its card
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    # One the server doesn't know: dismissing it fails as a request.
    When I inject a "learn.proposals" event for the channel with data:
      """
      {"proposals":[
        {"id":999999,"channel_id":"{channel_id}","learn_channel_id":"{learn_channel_id}","kind":"description","title":"Unknown to the server","rationale":"","payload":"{\"description\":\"x\"}","status":"failed","error":"boom","created_at":"{now-5s}","updated_at":"{now-5s}"}
      ]}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "Unknown to the server" to appear
    When I click on "[data-testid='learn-dismiss-all']"
    Then I wait up to "5s" for "[data-testid='learn-proposal'][data-status='pending']" to disappear
    And the element "[data-testid='learn-proposal'][data-status='dismissed']" should be visible
    And I wait for "[data-testid='learn-proposal'][data-status='failed'] [data-testid='learn-request-error']" to be visible
    # Only the failed one is left: Apply all leaves it for a Retry.
    And the element "[data-testid='learn-apply-all']" should not exist
    And the element "[data-testid='learn-dismiss-all']" should be visible
    # Nothing was applied.
    And the element "[data-testid='learn-proposal'][data-status='applied']" should not exist

  Scenario: A proposal a later pass withdraws stays listed, dimmed, with its reason
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for text "Name the thread after its work" to appear
    # A withdraw-only call: its learn.proposals event updates the open view.
    When the learn pass withdraws the proposal "Name the thread after its work" because "The work moved on to the login timeout."
    Then I wait for "[data-testid='learn-proposal'][data-status='withdrawn']" to be visible
    And the element "[data-testid='learn-proposal'][data-status='withdrawn'] [data-testid='learn-withdrawn-reason']" should contain text "The work moved on to the login timeout."
    And the element "[data-testid='learn-proposal'][data-status='withdrawn'] [data-testid='learn-apply']" should not exist
    And the element "[data-testid='learn-proposal'][data-status='withdrawn'] [data-testid='learn-dismiss']" should not exist
    # Apply all goes through the open one only.
    When I click on "[data-testid='learn-apply-all']"
    Then I wait up to "5s" for "[data-testid='learn-proposal'][data-status='pending']" to disappear
    And the element "[data-testid='learn-proposal'][data-status='applied']" should be visible
    And the element "[data-testid='learn-proposal'][data-status='withdrawn']" should be visible
    And the element "[data-testid='learn-apply-all']" should not exist

  Scenario: An applied shortcut proposal shows in the composer's # picker
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"prompt_shortcut","title":"Add a shortcut for the checks","payload":{"name":"bdd-learned-checks","prompt":"run the checks"}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I click on "[data-testid='learn-apply']"
    Then I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    When I click on "[data-testid='learn-close']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    When I type "#bdd-learned" into "[data-learn-chat-leaf] textarea"
    Then I wait for text "#bdd-learned-checks" to appear

  Scenario: What opens in the layout closes the Learn view; Escape a sidebar input takes doesn't
    Given the current channel has a learn thread
    And I create a file "notes/learn-target.md" in the repo with:
      """
      learnlinktarget
      """
    When I inject a bot message with:
      """
      See notes/learn-target.md for the notes.
      """
    Then I wait for "[data-learn-chat-leaf] a[href='#']" to be visible
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    # Escape clearing the sidebar search is the search's, not the view's.
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I type "zz" into "input[placeholder='Search...']"
    And I press Escape
    And I wait "300ms"
    Then the element "[data-testid='learn-split']" should be visible
    # A file link in the chat opens the editor in the layout, so the view
    # closes to show it.
    When I click on "[data-testid='learn-split-chat'] a[href='#']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for text "learnlinktarget" to appear
    # Adding a layout tab closes it too: the view holds the chat of the
    # layout it opened over.
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    When I click on "[title='New layout']"
    And I click on the button with text "Split"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for "[data-testid='layout-tab-Layout 1']" to be visible

  Scenario: A gate rule card shows the whole rule it applies
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"gate_rule","title":"Let rm clear scratch files","payload":{"type":"command","rule":{"commands":["rm"],"args_patterns":["^-rf /tmp/scratch"],"decision":"allow","message":"scratch only"}}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    # Not just the decision: what the rule matches and what it says.
    And I wait for text "command rule: allow rm with args matching ^-rf /tmp/scratch — “scratch only”" to appear

  Scenario: Focus moves on to the next open card as cards settle
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}},
        {"kind":"ticket_url","title":"Link the ticket","payload":{"ticket_url":"https://example.atlassian.net/browse/PROJ-9"}}
      ]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    When I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    # Newest first: the ticket, the description, the rename. Dismissing the
    # first moves focus to the next card's Apply, not to the page.
    When I click on "[data-testid='learn-dismiss'][aria-label='Dismiss “Link the ticket”']"
    Then I wait for "[data-testid='learn-proposal'][data-status='dismissed']" to be visible
    And the focus is in "[data-testid='learn-apply'][aria-label='Apply “Describe the thread”']"
    # The last card has none after it: focus goes to the nearest before.
    When I click on "[data-testid='learn-dismiss'][aria-label='Dismiss “Name the thread after its work”']"
    Then I wait up to "5s" for "[data-testid='learn-dismiss'][aria-label='Dismiss “Name the thread after its work”']" to disappear
    And the focus is in "[data-testid='learn-apply'][aria-label='Apply “Describe the thread”']"
    # Dismiss all leaves nothing open, and its button goes: focus goes to
    # the pane's close button.
    When I click on "[data-testid='learn-dismiss-all']"
    Then I wait up to "5s" for "[data-testid='learn-dismiss-all']" to disappear
    And the focus is in "[data-testid='learn-close']"

  Scenario: The learn thread's chat shows a reply that started while the view was closed, and opens its file links
    Given the current channel has a learn thread
    And I create a file "notes/learn-thread-target.md" in the repo with:
      """
      learnthreadlinktarget
      """
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    And I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run"}
      """
    Then I wait for "[data-testid='learn-badge']" to be visible
    # The user's reply runs while the view is closed; opening it shows the
    # run, not an idle composer.
    Given the learn thread has a bot message:
      """
      See notes/learn-thread-target.md for what changed.
      """
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"running","run_id":"bdd-learn-reply","trigger":"learn-reply"}
      """
    And I click on "[data-testid='learn-badge']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for "[data-learn-thread] button[title='Stop']" to be visible
    # A file link in the learn thread opens the parent's editor, and the
    # view closes to show it.
    When I click on "[data-learn-thread] a[href='#']"
    Then I wait up to "5s" for "[data-testid='learn-split']" to disappear
    And I wait for text "learnthreadlinktarget" to appear

  Scenario: On a canvas, the Learn pane docks beside the chat's tile and moves with it
    Given the current channel has a learn thread
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[{"kind":"description","title":"Describe the thread","payload":{"description":"Canvas dock"}}]}
      """
    Then the response status should be 201
    And I wait for "[data-testid='learn-badge']" to be visible
    # A new canvas starts empty; its first tile is the chat.
    When I click on "[title='New layout']"
    And I click on "[data-testid='new-layout-canvas']"
    And I click on "[data-testid='empty-layout-add-chat']"
    Then I wait for "[data-canvas-tile] [data-learn-chat-leaf] textarea" to be visible
    And I wait for "[data-canvas-tile] [data-testid='learn-badge']" to be visible
    # The Learn pane docks to the right of the chat's tile, the same size,
    # with the logo on the seam. The chat stays in its tile, not mounted
    # again, and nothing goes over the layout.
    When I tag the chat's composer
    And I click on "[data-canvas-tile] [data-testid='learn-badge']"
    Then I wait for "[data-testid='canvas-learn-dock'] [data-testid='learn-pane']" to be visible
    And the Learn dock sits right of the chat's tile, the same size
    And the element "[data-testid='learn-split']" should not exist
    And the composer in "[data-canvas-tile] [data-learn-chat-leaf]" is the one I tagged
    # Dragging the Learn pane's header moves the pair.
    When I note where the chat's tile is
    And I drag the Learn dock's header by -60px, 40px
    Then the chat's tile moved by -60px, 40px
    And the Learn dock sits right of the chat's tile, the same size
    # The badge in the chat's tile closes the dock; the chat stays.
    When I click on "[data-canvas-tile] [data-testid='learn-badge']"
    Then I wait up to "5s" for "[data-testid='canvas-learn-dock']" to disappear
    And the composer in "[data-canvas-tile] [data-learn-chat-leaf]" is the one I tagged

  Scenario: A turn's Learn button follows its pass, and shows the proposals it filed
    Given the current channel has a learn thread
    And the current channel has a finished turn "add a lint target" replying "Added a lint target."
    # Filed before the turn's pass: no turn of its own.
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[{"kind":"description","title":"An older proposal","payload":{"description":"Older"}}]}
      """
    Then the response status should be 201
    Given the turn has a "running" learn pass
    When I open the app in a browser
    And I click on "bdd-learn" in the sidebar
    Then I wait for "[data-testid='learn-turn'][data-status='running']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "Learning…"
    # At the end of the turn, below its reply, after its Explain button.
    And the element "[data-testid='learn-turn']" should be below "[data-msg-uuid]:not([data-is-user]) p"
    # Filed while the pass runs: they're the turn's.
    When I send a POST request to "/api/channels/{learn_channel_id}/learn/proposals" with body:
      """
      {"proposals":[
        {"kind":"rename","title":"Name the thread after its work","payload":{"name":"bdd-learn-renamed"}},
        {"kind":"description","title":"Describe the thread","payload":{"description":"Learn journey playground"}}
      ]}
      """
    Then the response status should be 201
    When I inject a "learn.pass" event for the channel with data:
      """
      {"id":999999,"channel_id":"{channel_id}","message_id":"{explain_msg_id}","learn_channel_id":"{learn_channel_id}","status":"done","created_at":"{now-5s}","updated_at":"{now-5s}"}
      """
    Then I wait for "[data-testid='learn-turn'][data-status='done'][data-open='true']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "2 proposals"
    # The button opens the Learn view on the turn: its cards are outlined,
    # not the older one.
    When I click on "[data-testid='learn-turn']"
    Then the Learn view shows the chat and the Learn pane side by side
    And I wait for "[data-testid='learn-proposal'][data-message-id^='explain-bdd-reply-'][data-highlighted='true']" to be visible
    And the element "[data-testid='learn-proposal']:not([data-message-id])" should contain text "An older proposal"
    And the element "[data-testid='learn-proposal']:not([data-message-id])[data-highlighted]" should not exist
    # Once none of its proposals waits, the button goes dim.
    When I click on "[data-testid='learn-dismiss'][aria-label='Dismiss “Describe the thread”']"
    And I click on "[data-testid='learn-dismiss'][aria-label='Dismiss “Name the thread after its work”']"
    Then I wait for "[data-testid='learn-turn'][data-open='false']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "2 proposals"
    # A newer turn's pass superseding this one's before it ran: the button
    # offers to learn from the turn again.
    When I inject a "learn.pass" event for the channel with data:
      """
      {"id":1000000,"channel_id":"{channel_id}","message_id":"{explain_msg_id}","learn_channel_id":"{learn_channel_id}","status":"superseded","created_at":"{now-1s}","updated_at":"{now-1s}"}
      """
    Then I wait for "[data-testid='learn-turn'][data-status='']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "Learn"

  Scenario: A turn without a learn pass offers one, and a failed request shows on the button
    Given the current channel has a finished turn "add a lint target" replying "Added a lint target."
    When I open the app in a browser
    And I click on "bdd-learn" in the sidebar
    # Learn is off, so no pass reviewed the turn: its button offers one.
    Then I wait for "[data-testid='learn-turn'][data-status='']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "Learn"
    And the element "[data-testid='learn-turn']" should be below "[data-msg-uuid]:not([data-is-user]) p"
    # The channel never ran an agent, so there's no session to fork.
    When I click on "[data-testid='learn-turn']"
    Then I wait for "[data-testid='learn-turn'][data-status='error']" to be visible
    And the element "[data-testid='learn-turn']" should contain text "Learn failed"
    And I wait for "[data-testid='learn-split'][data-view='learn'] [data-testid='learn-pane']" to be visible
    And the element "[data-testid='learn-turn-error']" should contain text "no session"
    # A click tries again.
    When I click on "[data-testid='learn-turn']"
    Then I wait for "[data-testid='learn-turn'][data-status='error']" to be visible
