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
