@frontend
Feature: Pane Maximize Journey
  A pane added from a maximized pane's header shows at once.

  @pane-maximize
  Scenario: Adding a pane from a maximized pane restores the layout
    Given I set up a test channel via API for directory "/tmp/bdd-pane-maximize"
    And I open the app in a browser
    And I wait for text "bdd-pane-maximize" to appear
    When I click on "bdd-pane-maximize" in the sidebar
    Then I wait for "textarea" to be visible

    When I click on "button[title='Expand pane']"
    Then I wait for "button[title='Restore pane']" to be visible

    # The new pane goes beside the maximized one, so the layout comes back
    # with both, rather than the new pane hidden until a tab switch.
    When I click on "button[title='Add panel']"
    And I add the "Notes" panel below in the menu
    Then I wait for "[data-testid='notes-panel']" to be visible
    And the element "button[title='Restore pane']" should not exist
    And I wait for "textarea" to be visible
