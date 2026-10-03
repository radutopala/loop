@frontend @continue
Feature: Continue button in the composer
  Once the chat has messages and the agent isn't running, a Continue button
  sends "continue" in one click, leaving whatever is typed in the composer.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-continue"
    And I open the app in a browser
    And I wait for text "bdd-continue" to appear
    When I click on "bdd-continue" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: Continue sends "continue" and keeps the draft
    When I inject 1 bot messages with content "earlier reply"
    Then I wait for "[data-testid='composer-continue']" to be visible
    When I type "half-written draft" into "textarea"
    And I click on "[data-testid='composer-continue']"
    Then I wait for "[data-msg-id][data-is-user]" to be visible
    And the element "[data-msg-id][data-is-user]" should contain text "continue"
    And the field "textarea" should hold "half-written draft"

  Scenario: Continue hides while the agent runs
    When I inject 1 bot messages with content "earlier reply"
    Then I wait for "[data-testid='composer-continue']" to be visible
    When I inject an agent.status running event
    Then I wait up to "5s" for "[data-testid='composer-continue']" to disappear
