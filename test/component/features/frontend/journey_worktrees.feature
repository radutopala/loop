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
    And I clear and type "25m" into "input[placeholder='30m']"
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
