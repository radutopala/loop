@frontend
Feature: Worktrees Journey
  End-to-end journey covering worktree creation from branch picker,
  thread navigation, worktree-scoped tasks, and global panel integration.

  Scenario: Branch picker and worktree creation
    Given I set up a test channel via API for git repo "bdd-mega-worktree"
    And I open the app in a browser
    And I wait for text "bdd-mega-worktree" to appear
    When I click on "bdd-mega-worktree" in the sidebar
    And I wait for "textarea" to be visible

    # Open branch picker, verify branches and search
    When I click on the button with title "Branch"
    Then I wait for text "BRANCHES" to appear
    And I wait for "input[placeholder*='Search']" to be visible
    And I wait for text "+wt" to appear

    # Create worktree from branch
    When I click on "+wt" in the branch picker
    Then I wait up to "15s" for text "worktree" to appear

  Scenario: Git panel worktrees tab shows entries and delete confirms before removing
    Given I set up a test channel via API for git repo "bdd-wt-panel"
    And I set up a worktree "panel-wt" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-wt-panel" to appear

    # Navigate to parent channel and open Git panel → Worktrees tab
    When I click on "bdd-wt-panel" in the sidebar
    And I wait for "textarea" to be visible
    When I add a "Git" panel
    And I click button "Worktrees" in the git panel
    Then I wait for text "panel-wt" to appear

    # Click Delete — confirmation popover should appear
    When I click button "Delete" in the worktrees panel
    Then I wait for text "Delete?" to appear

    # Dismiss with No
    When I click on the button with text "No"
    Then I wait for text "Delete?" to disappear
    And the page should contain text "panel-wt"

    # Click Delete again and confirm with Yes
    When I click button "Delete" in the worktrees panel
    Then I wait for text "Delete?" to appear
    When I click on the button with text "Yes"
    Then I wait up to "10s" for text "No worktrees" to appear

  Scenario: Non-imported worktree shows Import and Delete with confirmation
    Given I set up a test channel via API for git repo "bdd-wt-disk-only"
    And I create a disk-only git worktree "orphan-wt" on branch "main"
    And I open the app in a browser
    And I wait for text "bdd-wt-disk-only" to appear

    # Navigate to parent channel and open Git panel → Worktrees tab
    When I click on "bdd-wt-disk-only" in the sidebar
    And I wait for "textarea" to be visible
    When I add a "Git" panel
    And I click button "Worktrees" in the git panel
    Then I wait for text "orphan-wt" to appear

    # Non-imported worktree should show Import and Delete buttons
    And the page should contain text "Import"

    # Click Delete — confirmation popover should appear
    When I click button "Delete" in the worktrees panel
    Then I wait for text "Delete?" to appear

    # Dismiss with No
    When I click on the button with text "No"
    Then I wait for text "Delete?" to disappear
    And the page should contain text "orphan-wt"

    # Click Delete again and confirm with Yes — worktree removed from disk
    When I click button "Delete" in the worktrees panel
    Then I wait for text "Delete?" to appear
    When I click on the button with text "Yes"
    Then I wait up to "10s" for text "No worktrees" to appear

  Scenario: Worktree task lifecycle – create, global panel, Run Now, and delete
    Given I set up a test channel via API for git repo "bdd-wt-prompt-alphas"
    And I set up a worktree "mega-wt-test" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "mega-wt-test" to appear

    # Navigate to worktree thread
    When I click on "mega-wt-test" in the sidebar
    Then I wait for "textarea" to be visible

    # Open Tasks panel, verify empty
    When I add a "Tasks" panel
    Then I wait for text "0 tasks" to appear

    # Create task on worktree
    When I click the task create button
    And I select "interval" from "select"
    And I clear and type "25" into "[data-testid='task-interval-value']"
    And I type "wt-prompt-alpha" into "textarea[placeholder='Task prompt...']"
    And I click on the button with text "Create"
    Then I wait for text "wt-prompt-alpha" to appear

    # Verify in global panel
    When I open the global tasks panel
    Then I wait for text "wt-prompt-alpha" to appear

    # Return to worktree, verify Run Now
    When I click on "mega-wt-test" in the sidebar
    And I wait for "textarea" to be visible
    And I add a "Tasks" panel
    And I wait for text "wt-prompt-alpha" to appear
    When I click on "wt-prompt-alpha" in the tasks panel
    Then I wait for text "Run Now" to appear
    And the page should contain text "No runs yet"

    # Delete task
    When I click button "Delete" in the tasks panel
    Then I wait for text "0 tasks" to appear

  @wt-lock
  Scenario: Lock and unlock an imported worktree hides and restores Delete
    Given I set up a test channel via API for git repo "bdd-wt-lock"
    And I set up a worktree "lock-wt" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-wt-lock" to appear

    # Navigate to parent channel and open Git panel → Worktrees tab
    When I click on "bdd-wt-lock" in the sidebar
    And I wait for "textarea" to be visible
    When I add a "Git" panel
    And I click button "Worktrees" in the git panel
    Then I wait for text "Lock" to appear
    And the page should contain text "Delete"

    # Lock the worktree — Delete hides, button flips to Unlock
    When I click button "Lock" in the worktrees panel
    Then I wait for text "Unlock" to appear
    And the page should not contain text "Delete"

    # Unlock — Delete returns, button flips back to Lock
    When I click button "Unlock" in the worktrees panel
    Then I wait for text "Lock" to appear
    And the page should contain text "Delete"

  Scenario: Branches panel lists branches and deletes with confirmation
    Given I set up a test channel via API for git repo "bdd-branches-del"
    And I create a branch "feature/bdd-del-target" via API
    And I open the app in a browser
    And I wait for text "bdd-branches-del" to appear

    # Navigate to channel and open Git panel → Branches tab
    # After creating branch, current is "feature/bdd-del-target"; "main" is non-current
    When I click on "bdd-branches-del" in the sidebar
    And I wait for "textarea" to be visible
    When I add a "Git" panel
    And I click button "Branches" in the git panel
    Then I wait for text "main" to appear
    And I wait for text "Switch" to appear

    # Delete "main" — confirmation popover appears
    When I click button "Delete" in the branches panel
    Then I wait for text "Delete?" to appear

    # Dismiss with No
    When I click on the button with text "No"
    Then I wait for text "Delete?" to disappear
    And the page should contain text "main"

    # Delete again and confirm with Yes — "main" removed
    When I click button "Delete" in the branches panel
    Then I wait for text "Delete?" to appear
    When I click on the button with text "Yes"
    Then I wait up to "10s" for text "main" to disappear

  Scenario: Switch branch from branches panel updates header
    Given I set up a test channel via API for git repo "bdd-branches-switch"
    And I create a branch "feature/switch-target" via API
    And I open the app in a browser
    And I wait for text "bdd-branches-switch" to appear

    # Navigate to channel — header shows current branch
    When I click on "bdd-branches-switch" in the sidebar
    And I wait for "textarea" to be visible
    Then I wait for text "feature/switch-target" to appear

    # Open Git panel → Branches tab
    When I add a "Git" panel
    And I click button "Branches" in the git panel
    Then I wait for text "main" to appear
    And I wait for text "Switch" to appear

    # Click Switch on "main" — header should update immediately
    When I click button "Switch" in the branches panel
    Then I wait up to "10s" for text "feature/switch-target *" to disappear
