@frontend
Feature: Channels & Threads Journey
  End-to-end journey covering channel creation, selection, context menus,
  thread lifecycle, navigation, and deletion.

  Scenario: Channel and thread full lifecycle from creation through deletion
    Given I open the app in a browser
    And I wait for text "Settings" to appear

    # Cancel channel creation
    When I click on "+ new" in the sidebar
    And I click on "New project" in the sidebar
    And I type "should-not-create" into "input[placeholder='Channel name...']"
    And I press Escape
    Then the page should not contain text "should-not-create"

    # Create first channel via UI
    When I click on "+ new" in the sidebar
    And I click on "New project" in the sidebar
    And I type "bdd-mega-chan-a" into "input[placeholder='Channel name...']"
    And I press Enter
    Then I wait for text "bdd-mega-chan-a" to appear

    # Create second channel via API, verify both visible
    Given I set up a test channel via API for directory "/tmp/bdd-mega-chan-b"
    And I open the app in a browser
    And I wait for text "bdd-mega-chan-a" to appear
    Then the page should contain text "bdd-mega-chan-b"

    # Select channel, verify chat
    When I click on "bdd-mega-chan-b" in the sidebar
    Then I wait for "textarea" to be visible

    # Context menu on channel
    When I right-click on "bdd-mega-chan-b" in the sidebar
    Then the page should contain text "Copy Link"
    And the page should contain text "Copy Channel ID"
    And the page should contain text "Delete Channel"
    When I press Escape

    # Cancel thread creation
    When I hover over "bdd-mega-chan-b" in the sidebar
    And I click on the button with title "New thread"
    And I type "should-not-create" into "input[placeholder='Thread name...']"
    And I press Escape
    Then the page should not contain text "should-not-create"

    # Create two threads
    When I hover over "bdd-mega-chan-b" in the sidebar
    And I click on the button with title "New thread"
    And I type "mega-thread-one" into "input[placeholder='Thread name...']"
    And I press Enter
    Then I wait for text "mega-thread-one" to appear
    When I hover over "bdd-mega-chan-b" in the sidebar
    And I click on the button with title "New thread"
    And I type "mega-thread-two" into "input[placeholder='Thread name...']"
    And I press Enter
    Then I wait for text "mega-thread-two" to appear
    And the page should contain text "mega-thread-one"

    # Select thread, verify chat
    When I click on "mega-thread-one" in the sidebar
    Then I wait for "textarea" to be visible

    # Thread context menu and delete
    When I right-click on "mega-thread-one" in the sidebar
    Then the page should contain text "Delete Thread"
    When I click on "Delete Thread" in the context menu
    Then I wait for text "mega-thread-one" to disappear
    And the page should contain text "mega-thread-two"

    # Navigate between thread and parent
    When I click on "mega-thread-two" in the sidebar
    Then I wait for "textarea" to be visible
    When I click on "bdd-mega-chan-b" in the sidebar
    Then I wait for "textarea" to be visible

    # Open Tasks panel on channel
    When I add a "Tasks" panel
    Then I wait for text "No scheduled tasks" to appear

    # Delete channel
    When I right-click on "bdd-mega-chan-b" in the sidebar
    And I click on "Delete Channel" in the context menu
    Then I wait for text "bdd-mega-chan-b" to disappear
