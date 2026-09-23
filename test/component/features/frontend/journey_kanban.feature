@frontend @slow @kanban
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

  Scenario: Add a note to a ticket from the edit drawer
    Given I set up a test channel via API for git repo "kanban-notes"
    And I create a ticket "Flaky deploy" with type "bug" via API
    And I open the app in a browser

    And I click on "kanban-notes" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "Flaky deploy" to appear

    # The edit drawer lists notes, none yet
    And I click on "Flaky deploy" in the kanban panel
    And I wait for text "Edit Ticket" to appear
    And I wait for text "No notes yet" to appear

    # Adding one saves it straight away and lists it
    And I type "Retry fixed it on the second run" into "textarea[placeholder^='Add a note']"
    And I click button "Add note" in the kanban panel
    And I wait for text "Retry fixed it on the second run" to appear
    And I wait for text "No notes yet" to disappear

    # It survives closing and reopening the drawer
    And I click button "Cancel" in the kanban panel
    And I wait for text "Edit Ticket" to disappear
    And I click on "Flaky deploy" in the kanban panel
    And I wait for text "Retry fixed it on the second run" to appear

  Scenario: Kanban panel in a thread shows the project board
    Given I set up a test channel via API for git repo "kanban-thread"
    And I create a ticket "Fix login bug" with type "bug" via API
    And I create a thread "child-thread" under the current channel via API
    And I open the app in a browser

    # Navigate to thread
    And I click on "kanban-thread" in the sidebar
    And I wait for text "child-thread" to appear
    And I click on "child-thread" in the sidebar

    # The thread reads the same store as its channel
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "Fix login bug" to appear
    And the page should contain text "1 ticket"

  Scenario: Kanban panel in a worktree thread shows that worktree's board
    Given I set up a test channel via API for git repo "kanban-wt"
    And I create a ticket "Deploy pipeline" with type "task" via API
    And I set up a worktree "board-wt" on branch "main" under the current channel via API
    And I open the app in a browser

    # Navigate to the worktree thread
    And I click on "kanban-wt" in the sidebar
    And I wait for text "board-wt" to appear
    And I click on "board-wt" in the sidebar

    # The board follows the worktree's own checkout, which carries no .tickets/
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "0 tickets" to appear
    And the page should not contain text "Deploy pipeline"

    # Root switches to the board of the checkout the worktree was cut from
    And I click button "Root" in the kanban panel
    And I wait for text "Deploy pipeline" to appear
    And the page should contain text "1 ticket"

    # Local switches back to the worktree's own board
    And I click button "Local" in the kanban panel
    And I wait for text "0 tickets" to appear
    And the page should not contain text "Deploy pipeline"

  Scenario: Toolbar shows tk CLI tip
    Given I set up a test channel via API for git repo "kanban-tip"
    And I open the app in a browser

    And I click on "kanban-tip" in the sidebar
    And I wait for text "Kanban" to appear
    And I click on the element with text "Kanban"
    And I wait for text "0 tickets" to appear
    And the page should contain text "tk"
