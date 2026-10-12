@frontend @slow @terminal-continue
Feature: Continue button in the Docker Agent terminal
  While a Docker Agent pane's session is up, its footer shows the same »
  button as the chat composer. Clicking it types "continue" into the agent
  and presses Enter. Shell panes don't run Claude, so they don't get it.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-term-continue"
    And I open the app in a browser
    And I wait for text "bdd-term-continue" to appear
    When I click on "bdd-term-continue" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: Docker Agent pane shows Continue once its session runs
    When I add a "Docker Agent" panel
    Then I wait up to "60s" for "button[data-testid^='terminal-continue-docker-agent-']" to be visible
    When I rest the pointer on the element "button[data-testid^='terminal-continue-docker-agent-']"
    Then I wait for "[data-testid='hover-tip']" to be visible
    And the element "[data-testid='hover-tip']" should contain text "carries on where it stopped"
    # Without Claude auth the test env's claude exits and leaves the pane's
    # shell, so the typed "continue" reaches bash, which answers it.
    When I click on "button[data-testid^='terminal-continue-docker-agent-']"
    Then I wait up to "15s" for text "only meaningful in a" to appear

  Scenario: Host Shell pane has no Continue button
    When I add a "Host Shell" panel
    And I wait "3s"
    Then the element "button[data-testid^='terminal-continue-']" should not exist
