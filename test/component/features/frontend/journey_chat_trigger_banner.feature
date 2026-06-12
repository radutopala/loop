@frontend
Feature: Chat Trigger Banner Journey
  Floating trigger-quote banner that appears when the user's prompt scrolls
  off the top of the chat viewport while bot output keeps streaming below.

  @trigger-banner
  Scenario: Trigger banner shows when the prompt scrolls off the top
    Given I set up a test channel via API for git repo "bdd-trigger-banner"
    And I open the app in a browser
    And I wait for text "bdd-trigger-banner" to appear

    When I click on "bdd-trigger-banner" in the sidebar
    And I wait for "textarea" to be visible

    # Seed a user prompt + enough bot output to fill the viewport
    When I inject a user message with content "TRIGGER_BANNER_PROMPT"
    And I inject 40 bot messages with content "bot reply line"
    Then I wait for text "bot reply line" to appear

    # The chat auto-follows new output to the bottom, so scroll back to the top:
    # the prompt comes into view and the floating banner hides (driven by an
    # IntersectionObserver, so poll for it to disappear rather than asserting once).
    When I scroll the chat messages to top
    Then I wait up to "5s" for "[data-testid='trigger-quote']" to disappear

    # Scroll the chat container down — the prompt slides above the viewport
    # and the floating banner appears with the quoted prompt
    When I scroll the chat messages to bottom
    Then I wait for text "TRIGGER_BANNER_PROMPT" to appear
    And the element "[data-testid='trigger-quote']" should be visible
