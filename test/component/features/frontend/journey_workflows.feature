@frontend
Feature: Workflows
  End-to-end journeys covering the global workflows panel, the embedded
  split-panel variant, workflow-type scheduled tasks, delete/retry actions,
  and the default Workflows layout tab.

  Scenario: Global panel — open, empty state, start dialog, and close
    Given I open the app in a browser
    And I wait for text "Settings" to appear

    # Verify sidebar shows Workflows button
    And the page should contain text "Workflows"

    # Open workflows panel from sidebar
    When I open the workflows panel
    Then I wait for text "No workflow runs" to appear
    And I wait for text "+ Run" to appear

    # Open start workflow dialog
    When I click button "+ Run" in the workflows panel
    Then I wait for text "Start Workflow" to appear
    And the page should contain text "bdd-test-workflow"
    And the page should contain text "message"

    # Close start dialog via Cancel
    When I click on the button with text "Cancel"
    Then I wait for text "Start Workflow" to disappear

    # Close panel via close button
    When I click on the button with title "Close panel"
    Then I wait for text "No workflow runs" to disappear

  Scenario: Split panel — add, empty state, start run, view detail
    Given I set up a test channel via API for directory "/tmp/bdd-wf-split"
    And I open the app in a browser
    And I wait for text "bdd-wf-split" to appear

    # Navigate to channel and add Workflows split panel
    When I click on "bdd-wf-split" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Workflows" panel
    Then I wait for text "0 runs" to appear
    And the page should contain text "No workflow runs"

    # Open start dialog, verify workflow definition appears
    When I click button "+" in the workflows split panel
    Then I wait for text "Start Workflow" to appear
    And the page should contain text "bdd-test-workflow"
    And the page should contain text "message"

    # Cancel dialog — verify empty state persists
    When I click on the button with text "Cancel"
    Then I wait for text "Start Workflow" to disappear
    And the page should contain text "No workflow runs"

    # Start a workflow run
    When I click button "+" in the workflows split panel
    And I wait for text "Start Workflow" to appear
    And I click on the button with text "Start"
    Then I wait for text "Start Workflow" to disappear

    # Verify run appears and completes
    And I wait for text "bdd-test-workflow" to appear
    And I wait up to "90s" for text "COMPLETED" to appear

    # Click run to see detail
    When I click on "bdd-test-workflow" in the workflows split panel
    Then I wait for text "COMPLETED" to appear

  Scenario: Workflow tasks in per-channel and global panels
    # Seed channel with one regular task and one workflow task
    Given I set up a test channel via API for directory "/tmp/bdd-wf-tasks"
    And I set up a test task via API with type "interval" prompt "regular-prompt-task" and schedule "30m"
    And I set up a workflow task via API with workflow "fix-issue" and schedule "1h" with inputs:
      """
      {"issue_url":"https://github.com/test/repo/issues/1"}
      """
    And I open the app in a browser
    And I wait for text "bdd-wf-tasks" to appear

    # Open per-channel Tasks panel, verify both tasks appear
    When I click on "bdd-wf-tasks" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    Then I wait for text "2 tasks" to appear
    And the page should contain text "regular-prompt-task"
    And the page should contain text "workflow: fix-issue"

    # Select workflow task, verify detail shows workflow info
    When I click on "fix-issue" in the tasks panel
    Then I wait for text "Task #" to appear
    And the page should contain text "Workflow:"
    And the page should contain text "fix-issue"
    And the page should contain text "Inputs:"
    And the page should contain text "issue_url"
    And the page should contain text "RUN HISTORY"
    And the page should contain text "No runs yet"
    And the page should contain text "Run Now"

    # Toggle disable workflow task
    When I click on the button with text "Disable"
    Then I wait up to "15s" for text "Enable" to appear

    # Re-enable
    When I click on the button with text "Enable"
    Then I wait for text "Disable" to appear

    # Delete workflow task
    When I click on the button with text "Delete"
    Then I wait for text "fix-issue" to disappear
    And I wait for text "1 task" to appear

    # Verify regular task still present
    And the page should contain text "regular-prompt-task"

    # Select regular task to verify it shows prompt (not workflow)
    When I click on "regular-prompt-task" in the tasks panel
    Then I wait for text "Task #" to appear
    And the page should contain text "regular-prompt-task"
    And the page should not contain text "Workflow:"

    # Clean up regular task
    When I click on the button with text "Delete"
    Then I wait for text "regular-prompt-task" to disappear

    # Verify workflow task in global panel
    # Seed a fresh workflow task for global panel test
    Given I set up a workflow task via API with workflow "code-review" and schedule "2h"
    When I open the global tasks panel
    Then I wait up to "45s" for text "workflow: code-review" to appear
    And the page should contain text "bdd-wf-tasks"

    # Select in global panel, verify detail
    When I click on "code-review" in the global tasks panel
    Then I wait for text "Task #" to appear
    And the page should contain text "Workflow:"
    And the page should contain text "code-review"

    # Delete from global panel
    When I click on the button with text "Delete"
    Then I wait for text "code-review" to disappear

  Scenario: Split panel — delete a completed run
    Given I set up a test channel via API for directory "/tmp/bdd-wf-delete"
    And I open the app in a browser
    And I wait for text "bdd-wf-delete" to appear

    # Navigate to channel and add Workflows split panel
    When I click on "bdd-wf-delete" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Workflows" panel
    Then I wait for text "0 runs" to appear

    # Start a workflow run
    When I click button "+" in the workflows split panel
    And I wait for text "Start Workflow" to appear
    And I click on the button with text "Start"
    Then I wait for text "Start Workflow" to disappear

    # Wait for run to complete (allow time for Docker fallback timeout)
    And I wait for text "bdd-test-workflow" to appear
    And I wait up to "90s" for text "COMPLETED" to appear

    # Select the run to see detail with Delete button
    When I click on "bdd-test-workflow" in the workflows split panel
    Then I wait for text "Delete" to appear

    # Delete the run — confirm the popover, then list should return to empty
    When I click button "Delete" in the workflows split panel
    And I wait for text "Delete?" to appear
    And I click button "Yes" in the workflows split panel
    Then I wait for text "No workflow runs" to appear
    And the page should contain text "0 runs"

  Scenario: Split panel — retry a completed run
    Given I set up a test channel via API for directory "/tmp/bdd-wf-retry"
    And I open the app in a browser
    And I wait for text "bdd-wf-retry" to appear

    # Navigate to channel and add Workflows split panel
    When I click on "bdd-wf-retry" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Workflows" panel
    Then I wait for text "0 runs" to appear

    # Start a workflow run
    When I click button "+" in the workflows split panel
    And I wait for text "Start Workflow" to appear
    And I click on the button with text "Start"
    Then I wait for text "Start Workflow" to disappear

    # Wait for run to complete (allow time for Docker fallback timeout)
    And I wait for text "bdd-test-workflow" to appear
    And I wait up to "90s" for text "COMPLETED" to appear
    And the page should contain text "1 run"

    # Select the completed run to see Retry button
    When I click on "bdd-test-workflow" in the workflows split panel
    Then I wait for text "Retry" to appear

    # Retry the run — a second run should appear
    When I click button "Retry" in the workflows split panel
    Then I wait up to "90s" for text "2 runs" to appear

  Scenario: Default Workflows tab in channel layout
    Given I set up a test channel via API for directory "/tmp/bdd-wf-tab"
    And I open the app in a browser
    And I wait for text "bdd-wf-tab" to appear

    # Navigate to channel — Workflows tab should exist as a default layout
    When I click on "bdd-wf-tab" in the sidebar
    And I wait for "textarea" to be visible

    # Verify the Workflows layout tab exists in the tab bar
    And I wait for "[data-testid=layout-tab-Workflows]" to be visible

    # Click the Workflows layout tab
    When I click on "[data-testid=layout-tab-Workflows]"
    Then I wait for text "No workflow runs" to appear
    And the page should contain text "0 runs"

  Scenario: Global panel — rows show channel pill and dir_path; pill links to channel
    Given I set up a test channel via API for directory "/tmp/bdd-wf-global"
    And I open the app in a browser
    And I wait for text "bdd-wf-global" to appear

    # Seed a completed run via the split panel so the global panel has data to show
    When I click on "bdd-wf-global" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Workflows" panel
    And I click button "+" in the workflows split panel
    And I wait for text "Start Workflow" to appear
    And I click on the button with text "Start"
    Then I wait for text "Start Workflow" to disappear
    And I wait for text "bdd-test-workflow" to appear
    And I wait up to "90s" for text "COMPLETED" to appear

    # Open the global workflows panel — the row is enriched with channel pill and dir_path
    When I open the workflows panel
    Then I wait for text "#bdd-wf-global" to appear
    And the page should contain text "/tmp/bdd-wf-global"

    # Clicking the channel pill closes the global panel and focuses the channel
    When I click on "#bdd-wf-global" in the workflows panel
    Then the element "[data-testid=workflows-panel]" should not exist
    And the element "[data-testid=workflows-split-panel]" should be visible

  Scenario: Slash command autocomplete shows workflow commands
    Given I set up a test channel via API for directory "/tmp/bdd-wf-cmd"
    And I open the app in a browser
    And I wait for text "bdd-wf-cmd" to appear

    # Navigate to channel
    When I click on "bdd-wf-cmd" in the sidebar
    And I wait for "textarea" to be visible

    # Type /loop to trigger the command picker with all commands
    When I type "/loop " into "textarea"
    Then I wait for text "/workflows" to appear

    # Narrow down to workflow commands by typing the prefix
    When I clear and type "/loop workflow" into "textarea"
    Then I wait for text "/workflows" to appear
    And the page should contain text "List available workflows"
    And the page should contain text "/workflow-run"
    And the page should contain text "Run a workflow"
    And the page should contain text "/workflow-runs"
    And the page should contain text "List recent workflow runs"
    And the page should contain text "/workflow-cancel"
    And the page should contain text "/workflow-retry"
    And the page should contain text "/workflow-delete"

    # Dismiss the picker
    When I press Escape
    Then I wait for text "List available workflows" to disappear
