@frontend @composer-grow
Feature: The chat composer grows with its text
  The composer starts at three lines, grows as the message gets longer (up to
  a cap, then scrolls), and shrinks back once the message is sent. Editing a
  long queued message opens it already tall.

  Background:
    Given I set up a test channel via API for git repo "bdd-composer-grow"
    And I open the app in a browser
    And I wait for text "bdd-composer-grow" to appear
    When I click on "bdd-composer-grow" in the sidebar
    And I wait for "textarea" to be visible
    And I wait for "textarea" to be at most 90px tall

  Scenario: A long message grows the composer and sending shrinks it back
    When I type "grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow" into "textarea"
    Then I wait for "textarea" to be at least 150px tall
    When I press Enter
    Then the field "textarea" should hold ""
    And I wait for "textarea" to be at most 90px tall

  # The channel is empty, so the first message flips the chat out of its empty
  # state. The message's own event can land before the send request returns;
  # holding the request back makes that order certain. The composer must not
  # be swapped for a new one there, or the new one brings the sent text back.
  Scenario: The first message clears the composer even when its event lands first
    Given I delay requests matching "*/api/messages" by "2s"
    When I type "first message" into "textarea"
    And I mark the element "textarea"
    And I press Enter
    And I inject a user message with content "first message"
    Then I wait for text "first message" to appear
    And the field "textarea" should hold ""
    And I wait for "textarea[data-bdd-mark='1']" to be visible

  Scenario: Editing a long queued message opens the composer tall
    When I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"{channel_id}","content":"grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow grow","delay_seconds":3600}
      """
    And I wait for "[data-testid='queued-toggle']" to be visible
    And I click on "[data-testid='queued-toggle']"
    And I click on "[data-testid='queued-edit']"
    Then I wait for "[data-testid='queued-edit-banner']" to be visible
    And I wait for "textarea" to be at least 150px tall
