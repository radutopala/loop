@frontend @send-mode
Feature: Composer send modes
  The chip beside the send button picks how a message sent during a run is
  delivered: queued behind the run, added to its running turn, or steering it.
  The choice is kept across reloads, and a lone ⌘ (Ctrl) tap in the
  composer cycles through the modes.

  Scenario: Pick the Add send mode
    Given I set up a test channel via API for git repo "bdd-send-mode"
    And I open the app in a browser
    And I wait for text "bdd-send-mode" to appear
    When I click on "bdd-send-mode" in the sidebar
    And I wait for "textarea" to be visible
    Then the element "[data-testid='send-mode-chip']" should contain text "QUEUE"

    When I click on "[data-testid='send-mode-chip']"
    And I wait for "[data-testid='send-mode-add']" to be visible
    And I click on "[data-testid='send-mode-add']"
    Then the element "[data-testid='send-mode-chip']" should contain text "ADD"
    And the element "[data-testid='send-mode-add']" should not exist

    When I open the app in a browser
    And I wait for text "bdd-send-mode" to appear
    And I click on "bdd-send-mode" in the sidebar
    And I wait for "textarea" to be visible
    Then the element "[data-testid='send-mode-chip']" should contain text "ADD"

  Scenario: A lone modifier tap cycles the send modes
    Given I set up a test channel via API for git repo "bdd-send-cycle"
    And I open the app in a browser
    And I wait for text "bdd-send-cycle" to appear
    When I click on "bdd-send-cycle" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "textarea"
    Then the element "[data-testid='send-mode-chip']" should contain text "QUEUE"
    When I tap the send mode cycle key
    Then the element "[data-testid='send-mode-chip']" should contain text "ADD"
    When I tap the send mode cycle key
    Then the element "[data-testid='send-mode-chip']" should contain text "STEER"
    When I tap the send mode cycle key
    Then the element "[data-testid='send-mode-chip']" should contain text "QUEUE"

  @add-to-turn
  Scenario: A message added to the running turn shows as adding until taken in
    Given I set up a test channel via API for git repo "bdd-send-adding"
    And the current channel has "read this mid-turn" added to its running turn
    And I open the app in a browser
    And I wait for text "bdd-send-adding" to appear
    When I click on "bdd-send-adding" in the sidebar
    And I wait for "textarea" to be visible
    And I inject an agent.status running event
    And I wait for "[data-testid='queued-toggle']" to be visible
    And I click on "[data-testid='queued-toggle']"
    And I wait for "[data-testid='queued-adding']" to be visible
    Then the element "[data-testid='queued-row'][data-adding='true']" should contain text "read this mid-turn"
    And the element "[data-testid='queued-steer']" should not exist
    And the element "[data-testid='queued-edit']" should not exist
    And the page should contain text "adding…"

    When the agent takes in the added message
    Then I wait up to "5s" for "[data-testid='queued-toggle']" to disappear
    And I wait up to "5s" for text "adding…" to disappear
    And the page should contain text "read this mid-turn"
