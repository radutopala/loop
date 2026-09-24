@frontend @slow @stop-resync
Feature: Stop button resyncs with the backend
  A run whose "done" events never reached the app leaves Stop on. Opening
  the channel refetches the channel list; when that fetch started after the
  run's "running" event and reports no agent running, Stop clears.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-stop-resync"
    And I open the app in a browser
    And I wait for text "bdd-stop-resync" to appear
    And I click on "bdd-stop-resync" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: A leftover Stop clears when the channel is opened again
    # The injected run has no backend counterpart and no "done" event, the
    # same state a missed agent.status "completed" leaves behind. A served
    # history keeps the chat (not the welcome screen, whose composer has no
    # Stop) across the reopen; the injected message shows it right away.
    When I serve a chat history of 3 messages from the timeline
    And I inject a user message with content "stop-resync prompt"
    And I inject an agent.status running event
    Then I wait for "button[title='Stop']" to be visible
    # Reopening remounts the chat from the stored (running) state and
    # refetches the channel list, which says nothing is running.
    When I click on "bdd-stop-resync" in the sidebar
    Then I wait for text "history message 2" to appear
    And I wait up to "10s" for "button[title='Stop']" to disappear
    And the element "textarea" should be visible
