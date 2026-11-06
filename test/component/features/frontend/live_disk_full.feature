Feature: A live session that ends on a full disk

  Scenario: The bar shows the process's error and offers to reclaim Docker space
    Given I set up a test channel via API for git repo "bdd-live-disk-full"
    And I open the app in a browser
    And I wait for text "bdd-live-disk-full" to appear

    When I click on "bdd-live-disk-full" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a "live.status" event for the channel with data:
      """
      {"running": false, "busy": false, "reason": "exited: Error processing settings: the volume holding /tmp is full (ENOSPC)."}
      """
    Then I wait for "[data-testid='live-bar-reclaim']" to be visible
    And the element "[data-testid='live-bar-reason']" should contain text "the volume holding /tmp is full (ENOSPC)."

    When I click on "[data-testid='live-bar-reclaim-settings']"
    Then I wait for "[data-testid='reclaim-space']" to be visible

  Scenario: Another error offers no reclaim
    Given I set up a test channel via API for git repo "bdd-live-other-error"
    And I open the app in a browser
    And I wait for text "bdd-live-other-error" to appear

    When I click on "bdd-live-other-error" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a "live.status" event for the channel with data:
      """
      {"running": false, "busy": false, "reason": "exited: boom"}
      """
    Then the element "[data-testid='live-bar-reason']" should contain text "it exited: boom"
    And the element "[data-testid='live-bar-reclaim']" should not exist
