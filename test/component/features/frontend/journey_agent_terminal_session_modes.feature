@frontend
Feature: Agent Terminal Session Mode Picker Journey
  End-to-end journey verifying that the docker-agent panel split menu
  exposes three session-handling variants — Resume, Resume with fork,
  Fresh session — so users pick up-front how a docker-agent pane boots
  Claude relative to the channel's stored session.

  Scenario: Pane-split menu lists Docker Agent session-mode variants
    Given I set up a test channel via API for directory "/tmp/bdd-agent-modes-menu"
    And I open the app in a browser
    And I wait for text "bdd-agent-modes-menu" to appear
    When I click on "bdd-agent-modes-menu" in the sidebar
    Then I wait for "textarea" to be visible

    # Open the "+" pane-split menu on a pane header.  The first matching
    # button in the DOM is the chat pane's header button — any pane header
    # works since the menu content is the same.
    When I click on "button[title='Add panel']"
    Then the element "[data-testid='add-panel-menu']" should be visible

    # Three docker-agent variants each appear as separate menu entries,
    # rendered as "Docker Agent (<mode>) ↓" / "→".  Substring assertions
    # discriminate the modes — "(Resume)" closes immediately after Resume,
    # so it does not match "(Resume with fork)".
    And the page should contain text "Docker Agent (Resume)"
    And the page should contain text "Docker Agent (Resume with fork)"
    And the page should contain text "Docker Agent (Fresh session)"

  Scenario: Adding a Docker Agent (Fresh session) variant mounts a pane
    Given I set up a test channel via API for directory "/tmp/bdd-agent-modes-fresh"
    And I open the app in a browser
    And I wait for text "bdd-agent-modes-fresh" to appear
    When I click on "bdd-agent-modes-fresh" in the sidebar
    Then I wait for "textarea" to be visible

    # The addPanel step matches by textContent.includes, so the variant label
    # "Docker Agent (Fresh session)" picks the fresh-mode entry.  After the
    # click the dropdown closes and the menu element should be gone.
    When I add a "Docker Agent (Fresh session)" panel
    Then I wait for text "Docker Agent (Fresh session)" to disappear
