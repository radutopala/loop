@frontend @slow
Feature: Per-Channel Tasks Journey
  End-to-end journey covering task panel CRUD, detail view, edit, toggle,
  Run Now, delete, task threads, stop button, and thread-scoped tasks.

  Scenario: Task lifecycle from empty panel through run history and thread tasks
    Given I set up a test channel via API for directory "/tmp/bdd-mega-tasks"
    And I create a thread "regular-thread" under the current channel via API
    And I create a thread "task-thread-mega" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-mega-tasks" to appear

    # Task thread sidebar display and hierarchy
    Then I wait for text "regular-thread" to appear
    And the page should contain text "task-thread-mega"

    # Navigate to task thread, verify chat and no stop button
    When I click on "task-thread-mega" in the sidebar
    Then I wait for "textarea" to be visible
    And the element "button[title='Stop']" should not exist

    # Task thread context menu
    When I right-click on "task-thread-mega" in the sidebar
    Then the page should contain text "Delete Thread"

    # Delete task thread
    When I click on "Delete Thread" in the context menu
    Then I wait for text "task-thread-mega" to disappear

    # Select channel, open Tasks panel, verify empty
    When I click on "bdd-mega-tasks" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    Then I wait for text "0 tasks" to appear
    And the page should contain text "No scheduled tasks"
    And the element "button[title='Stop']" should not exist

    # Cancel task creation
    When I click the task create button
    And I wait for "textarea[placeholder='Task prompt...']" to be visible
    And I click on the button with text "Cancel"
    Then the element "textarea[placeholder='Task prompt...']" should not exist

    # Create interval task
    When I click the task create button
    And I select "interval" from "select"
    And I clear and type "15m" into "input[placeholder='30m']"
    And I type "mega-interval-task" into "textarea[placeholder='Task prompt...']"
    And I click on the button with text "Create"
    Then I wait for text "mega-interval-task" to appear
    And I wait for text "1 task" to appear

    # Create cron task
    When I click the task create button
    And I select "cron" from "select"
    And I clear and type "*/5 * * * *" into "input[placeholder='*/30 * * * *']"
    And I type "mega-cron-task" into "textarea[placeholder='Task prompt...']"
    And I click on the button with text "Create"
    Then I wait for text "mega-cron-task" to appear
    And I wait for text "2 tasks" to appear

    # Select task, verify detail view
    When I click on "mega-interval-task" in the tasks panel
    Then I wait for text "Run Now" to appear
    And the page should contain text "Edit"
    And the page should contain text "Delete"
    And the page should contain text "RUN HISTORY"
    And the page should contain text "No runs yet"

    # Edit task prompt
    When I click on the button with text "Edit"
    And I wait for text "Edit Task #" to appear
    And I clear and type "mega-updated-prompt" into "textarea[rows='5']"
    And I click on the button with text "Save"
    Then I wait for text "mega-updated-prompt" to appear

    # Cancel edit
    When I click on the button with text "Edit"
    And I wait for text "Edit Task #" to appear
    And I click on the button with text "Cancel"
    Then I wait for text "Run Now" to appear

    # Edit task schedule (change type to cron with a safe daily schedule)
    When I click on the button with text "Edit"
    And I wait for text "Edit Task #" to appear
    And I select "cron" from "select"
    And I clear and type "0 0 1 1 *" into "select + input"
    And I click on the button with text "Save"
    Then I wait for text "mega-updated-prompt" to appear
    And I wait for text "Run Now" to appear

    # Toggle disable/enable
    And I click on the button with text "Disable"
    Then I wait for text "Enable" to appear
    When I click on the button with text "Enable"
    Then I wait for text "Disable" to appear
    And the page should contain text "Run Now"

    # Trigger Run Now via API, wait for completion, re-select task to see results
    When I capture the visible task ID
    And I trigger Run Now via API for the current task
    And I wait up to "120s" for the task to stop running via API
    # Re-select the task to force run history reload
    When I click on "mega-cron-task" in the tasks panel
    And I click on "mega-updated-prompt" in the tasks panel
    Then I wait for text "RUN HISTORY" to appear
    And I wait for text "No runs yet" to disappear
    And the page should contain text "Run Now"

    # Delete task
    When I click on the button with text "Delete"
    Then I wait for text "mega-updated-prompt" to disappear
    When I click on "mega-cron-task" in the tasks panel
    And I click on the button with text "Delete"
    Then I wait for text "mega-cron-task" to disappear
    And I wait for text "0 tasks" to appear

    # Thread-scoped task lifecycle
    When I click on "regular-thread" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    Then I wait for text "0 tasks" to appear
    When I click the task create button
    And I select "interval" from "select"
    And I clear and type "20m" into "input[placeholder='30m']"
    And I type "mega-thread-task" into "textarea[placeholder='Task prompt...']"
    And I click on the button with text "Create"
    Then I wait for text "mega-thread-task" to appear

    # Verify in global panel
    When I open the global tasks panel
    Then I wait for text "mega-thread-task" to appear

    # Return to thread and delete
    When I click on "regular-thread" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    And I wait for text "mega-thread-task" to appear
    And I click on "mega-thread-task" in the tasks panel
    And I click on the button with text "Delete"
    Then I wait for text "mega-thread-task" to disappear
