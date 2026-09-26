@frontend @queue-edit
Feature: Edit a queued chat message
  The pencil on a queued row moves the message into the composer; Enter saves
  it back in place, Escape leaves it untouched. While the edit is open the
  message holds its place and can't start.

  Background:
    Given I set up a test channel via API for git repo "bdd-queue-edit"
    And I open the app in a browser
    And I wait for text "bdd-queue-edit" to appear
    When I click on "bdd-queue-edit" in the sidebar
    And I wait for "textarea" to be visible
    # A delayed message stays queued without an agent ever picking it up.
    And I send a POST request to "/api/messages" with body:
      """
      {"channel_id":"{channel_id}","content":"original queued text","delay_seconds":3600}
      """
    And I wait for "[data-testid='queued-toggle']" to be visible
    And I click on "[data-testid='queued-toggle']"
    And I wait for "[data-testid='queued-edit']" to be visible

  Scenario: Save puts the edited text back into the queue
    When I click on "[data-testid='queued-edit']"
    Then I wait for "[data-testid='queued-edit-banner']" to be visible
    And the field "textarea" should hold "original queued text"
    And the element "[data-testid='queued-row']" should contain text "editing"

    When I clear and type "edited queued text" into "textarea"
    And I press Enter
    Then I wait up to "5s" for "[data-testid='queued-edit-banner']" to disappear
    And the element "[data-testid='queued-row']" should contain text "edited queued text"
    And the field "textarea" should hold ""

  Scenario: Escape cancels and leaves the message as it was
    When I click on "[data-testid='queued-edit']"
    Then I wait for "[data-testid='queued-edit-banner']" to be visible
    When I clear and type "never saved" into "textarea"
    And I press Escape
    Then I wait up to "5s" for "[data-testid='queued-edit-banner']" to disappear
    And the element "[data-testid='queued-row']" should contain text "original queued text"

  Scenario: Removing the message mid-edit ends the edit but keeps the text
    When I click on "[data-testid='queued-edit']"
    Then I wait for "[data-testid='queued-edit-banner']" to be visible
    When I clear and type "keep this draft" into "textarea"
    And I remove every queued message via API
    Then I wait for "[data-testid='queued-edit-notice']" to be visible
    And the element "[data-testid='queued-edit-notice']" should contain text "left the queue"
    And the field "textarea" should hold "keep this draft"
