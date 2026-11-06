@frontend @message-origin
Feature: Messages from another channel
  A message an agent sends from another channel or thread shows where it came
  from, with a link that opens that channel.

  Scenario: The link on a message opens the channel that sent it
    Given I set up a test channel via API for git repo "bdd-message-origin"
    And I create a thread "origin-sender" under the current channel via API
    And the latest thread queues "note in the sender" for itself
    And the latest thread queues "question from the sender" for the current channel
    And I open the app in a browser
    And I wait for text "bdd-message-origin" to appear

    When I click on "bdd-message-origin" in the sidebar
    And I wait for text "question from the sender" to appear
    And I wait for text "from #origin-sender" to appear

    When I click on "[data-testid='message-origin']"
    Then I wait for text "note in the sender" to appear

  Scenario: The link to a deleted thread says so and stays on the channel
    Given I set up a test channel via API for git repo "bdd-message-origin-gone"
    And I create a thread "origin-gone" under the current channel via API
    And the latest thread queues "question from the gone thread" for the current channel
    And the latest thread is deleted via API
    And I open the app in a browser
    And I wait for text "bdd-message-origin-gone" to appear

    When I click on "bdd-message-origin-gone" in the sidebar
    And I wait for text "question from the gone thread" to appear
    And I wait for text "from #origin-gone" to appear

    When I click on "[data-testid='message-origin']"
    Then I wait for text "doesn't exist anymore" to appear
    And I wait for text "question from the gone thread" to appear
