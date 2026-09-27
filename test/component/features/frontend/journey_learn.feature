@frontend @learn
Feature: Learn from runs
  The composer's Learn switch turns a channel's learn pass on or off and
  sticks to the channel. A learn pass runs in a hidden learn thread and files
  proposals; the layouts bar's Learn badge says what it's doing and opens the
  Learn drawer, where each proposal is applied or dismissed.

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
    When I click on "[data-testid='learn-badge']"
    Then I wait for text "https://example.atlassian.net/browse/PROJ-7" to appear
    And the page should contain text "ticket"
    When I click on "[data-testid='learn-apply']"
    Then I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    And I wait for "[data-testid='header-ticket']" to be visible
    And the element "[data-testid='header-ticket']" should contain text "PROJ-7"

  Scenario: Proposals filed by a learn pass are dismissed and applied from the drawer
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
    # The layout tabs shrink before the bar overflows, so the badge at its
    # right end stays on screen even with every default layout open.
    And the element "[data-testid='learn-badge']" should fit inside the window
    # The drawer slides in from past the layout's right edge; nothing behind
    # it may scroll or shift meanwhile.
    When I open the Learn drawer and nothing behind it moves
    Then I wait for "[data-testid='learn-drawer']" to be visible
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
    Then I wait for "[data-testid='learn-proposal'][data-status='applied']" to be visible
    And the element "[data-testid='learn-proposal'][data-status='dismissed']" should be visible
    When I click on "[data-testid='learn-drawer-close']"
    Then I wait up to "5s" for "[data-testid='learn-drawer']" to disappear

  Scenario: The badge shows a running learn pass until its thread's run ends
    Given the current channel has a learn thread
    When I inject a "learn.started" event for the channel with data:
      """
      {"learn_channel_id":"{learn_channel_id}"}
      """
    Then I wait for "[data-testid='learn-badge'][data-running='true']" to be visible
    And the element "[data-testid='learn-badge']" should contain text "learning…"
    When I click on "[data-testid='learn-badge']"
    Then I wait for text "reviewing the last run…" to appear
    When I inject a "agent.status" event for the learn thread with data:
      """
      {"status":"completed","run_id":"bdd-learn-run"}
      """
    Then I wait for "[data-testid='learn-badge'][data-running='false']" to be visible
    And I wait for text "proposals from the last runs" to appear
    And the page should not contain text "learning…"
