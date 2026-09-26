@frontend @rowinfo
Feature: Sidebar Row Info
  Hovering a channel, thread or worktree thread in the sidebar immediately
  shows its directory, git branch, commit and subject in a popup beside the
  row. A worktree thread also names the branch it was cut from and how far it
  is from it, and a channel with its own model or effort shows them too.

  Scenario: A channel and its worktree thread show their path and branch
    Given I set up a test channel via API for git repo "bdd-row-info"
    And I set up a worktree "row-info-wt" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "bdd-row-info" to appear

    When I rest the pointer on "bdd-row-info" in the sidebar
    Then the row info should show the channel's path and branch "main"
    And the row info "commit" should read "{head}"
    And the row info "commit-detail" should read " initial commit"
    And the row info should not show "sync"
    And the row info should not show "model"

    # Picking a model updates the popup while it's open.
    When I send a PATCH request to "/api/channels/{channel_id}/agent-config" with body:
      """
      {"model": "claude-opus-5-5", "effort": "high"}
      """
    Then the row info "model" should read "claude-opus-5-5 · high"

    When I rest the pointer on "row-info-wt" in the sidebar
    Then the row info should show the worktree's path and branch "worktree/row-info-wt"
    And the row info "branch-detail" should read " (from main)"
    And the row info "commit" should read "{head}"
    And the row info "sync" should read "even with main"
    And the row info should not show "model"

    # A commit in the worktree shows up while the popup is open.
    When I commit "wt work" in the worktree
    Then the row info "commit-detail" should read " wt work"
    And the row info "sync" should read "↑1 vs main"

    When I move the pointer off the sidebar
    Then I wait up to "2s" for "[data-testid='sidebar-row-info']" to disappear

  @row-description
  Scenario: A thread's description shows in its row info and is set from the context menu
    Given I set up a test channel via API for git repo "bdd-row-desc"
    And I set up a worktree "desc-wt" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "desc-wt" to appear

    When I rest the pointer on "desc-wt" in the sidebar
    Then the row info should not show "description"

    When I right-click on "desc-wt" in the sidebar
    And I click on "Edit Description" in the context menu
    Then I wait for "[data-testid='description-dialog']" to be visible
    When I clear and type "Fixes the login redirect" into "[data-testid='description-input']"
    And I click on "[data-testid='description-submit']"
    Then I wait up to "2s" for "[data-testid='description-dialog']" to disappear

    When I rest the pointer on "desc-wt" in the sidebar
    Then the row info "description" should read "Fixes the login redirect"
    # Setting it keeps the git lines the popup already had.
    And the row info should show the worktree's path and branch "worktree/desc-wt"

    # The dialog opens with the current description to edit; saving it empty clears it.
    When I right-click on "desc-wt" in the sidebar
    And I click on "Edit Description" in the context menu
    Then I wait for "[data-testid='description-dialog']" to be visible
    And the field "[data-testid='description-input']" should hold "Fixes the login redirect"
    When I clear and type "" into "[data-testid='description-input']"
    And I click on "[data-testid='description-submit']"
    And I rest the pointer on "desc-wt" in the sidebar
    Then the row info should not show "description"

    # An agent sets it through the API; the open popup follows.
    When I rest the pointer on "bdd-row-desc" in the sidebar
    And I send a POST request to "/api/channels/{channel_id}/description" with body:
      """
      {"description": "Main checkout of the repo"}
      """
    Then the response status should be 200
    And the row info "description" should read "Main checkout of the repo"
    And the row info should show the channel's path and branch "main"
