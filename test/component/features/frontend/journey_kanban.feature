@frontend @slow
Feature: Kanban panel
  Ticket board with create, edit, and status transitions.

  Scenario: Kanban panel shows tickets grouped by status
    Given I set up a test channel via API for git repo "kanban"
    And I create a ticket "Fix login bug" with type "bug" via API
    And I create a ticket "Add search feature" with type "feature" via API
    And I open the app in a browser

    # Navigate to channel and switch to Kanban layout tab
    And I click on "kanban" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"

    # Verify tickets appear in the Open column
    And I wait for text "Fix login bug" to appear
    And the page should contain text "Add search feature"
    And the page should contain text "2 tickets"
    And the page should contain text "BUG"
    And the page should contain text "FEATURE"

  Scenario: Create a new ticket from the Kanban panel
    Given I set up a test channel via API for git repo "kanban-create"
    And I open the app in a browser

    # Navigate to channel and switch to Kanban tab
    And I click on "kanban-create" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "0 tickets" to appear

    # Open create modal and fill form
    And I click button "+ New" in the kanban panel
    And I wait for text "New Ticket" to appear
    And I type "Implement dark mode" into "input[placeholder='Title']"
    And I click button "Create" in the kanban panel

    # Verify ticket appears
    And I wait for text "Implement dark mode" to appear
    And the page should contain text "1 ticket"

  Scenario: Change ticket status from Open to In Progress
    Given I set up a test channel via API for git repo "kanban-status"
    And I create a ticket "Deploy pipeline" with type "task" via API
    And I open the app in a browser

    # Navigate to channel and switch to Kanban tab
    And I click on "kanban-status" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "Deploy pipeline" to appear

    # Move ticket to In Progress
    And I click button "Start" in the kanban panel
    And I wait for text "Close" to appear
    And I wait for text "Reopen" to appear

  Scenario: Edit a ticket from the Kanban panel
    Given I set up a test channel via API for git repo "kanban-edit"
    And I create a ticket "Original title" with type "task" via API
    And I open the app in a browser

    # Navigate to channel and switch to Kanban tab
    And I click on "kanban-edit" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "Original title" to appear

    # Click title to open edit modal
    And I click on "Original title" in the kanban panel
    And I wait for text "Edit Ticket" to appear

    # Update the title
    And I clear and type "Updated title" into "input[placeholder='Title']"
    And I click button "Save" in the kanban panel

    # Verify updated title
    And I wait for text "Updated title" to appear
    And the page should not contain text "Original title"

  Scenario: Kanban panel is not available in threads
    Given I set up a test channel via API for git repo "kanban-thread"
    And I create a thread "child-thread" under the current channel via API
    And I open the app in a browser

    # Navigate to thread
    And I click on "kanban-thread" in the sidebar
    And I wait for text "child-thread" to appear
    And I click on "child-thread" in the sidebar

    # Kanban tab should not be visible in thread
    And the page should not contain text "Kanban"

  Scenario: Toolbar shows tk CLI tip
    Given I set up a test channel via API for git repo "kanban-tip"
    And I open the app in a browser

    And I click on "kanban-tip" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "0 tickets" to appear
    And the page should contain text "tk"
