@frontend @rowinfo
Feature: Sidebar Row Info
  Hovering a channel, thread or worktree thread in the sidebar immediately
  shows its full name, directory, git branch, the PR open on it, commit and
  subject in a popup beside the row. A worktree thread also names the branch it was cut from and how far it
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
    And the row info should not show "pr"
    And the row info should not show "sync"
    And the row info should not show "model"

    # Picking a model updates the popup while it's open.
    When I send a PATCH request to "/api/channels/{channel_id}/agent-config" with body:
      """
      {"model": "claude-opus-5-5", "effort": "high"}
      """
    Then the row info "model" should read "claude-opus-5-5 · high"

    When I rest the pointer on "row-info-wt" in the sidebar
    Then the row info "title" should read "row-info-wt (main)"
    And the row info should show the worktree's path and branch "worktree/row-info-wt"
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

  @row-title
  Scenario: A name too long for the sidebar shows in full in the row info
    Given I set up a test channel via API for git repo "bdd-row-title"
    And I set up a worktree "a-worktree-thread-name-far-too-long-to-fit-in-the-sidebar-row" on branch "main" under the current channel via API
    And I open the app in a browser
    And I wait for text "a-worktree-thread-name" to appear

    When I rest the pointer on "a-worktree-thread-name" in the sidebar
    Then the row info "title" should read "a-worktree-thread-name-far-too-long-to-fit-in-the-sidebar-row (main)"

  @row-pr
  Scenario: The PR open on a channel's branch shows in its row info and the header
    Given I set up a test channel via API for git repo "bdd-row-pr"
    And the channel's repo has origin "https://github.com/o/r.git"
    And gh reports PR #42 "Fix the login redirect" for any branch
    And I open the app in a browser
    And I wait for text "bdd-row-pr" to appear
    And I click on "bdd-row-pr" in the sidebar

    Then the element "[data-testid='header-pr']" should contain text "PR #42"
    And I wait for "[data-testid='header-pr-state'][data-state='open']" to be visible

    When I rest the pointer on "bdd-row-pr" in the sidebar
    Then the row info "pr" should read "#42"
    And the row info "pr-detail" should read " Fix the login redirect (open)"
    And the row info should show the channel's path and branch "main"

  @row-mr
  Scenario: A GitLab repo's merge request shows as an MR
    Given I set up a test channel via API for git repo "bdd-row-mr"
    And the channel's repo has origin "https://gitlab.example.com/g/r.git"
    And glab reports MR !12 "Fix the logout redirect" for any branch
    And I open the app in a browser
    And I wait for text "bdd-row-mr" to appear
    And I click on "bdd-row-mr" in the sidebar

    Then the element "[data-testid='header-pr']" should contain text "MR !12"
    And I wait for "[data-testid='header-pr-state'][data-state='open']" to be visible

    When I rest the pointer on "bdd-row-mr" in the sidebar
    Then the row info "pr" should read "!12"
    And the row info "pr-detail" should read " Fix the logout redirect (open)"

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

  @row-ticket
  Scenario: A channel's ticket URL shows in its row info and is edited and opened from the context menu
    Given I set up a test channel via API for git repo "bdd-row-ticket"
    And I open the app in a browser
    And I wait for text "bdd-row-ticket" to appear
    And I click on "bdd-row-ticket" in the sidebar

    When I rest the pointer on "bdd-row-ticket" in the sidebar
    Then the row info should not show "ticket"
    And the element "[data-testid='header-ticket']" should not exist

    # Only absolute http(s) URLs are taken.
    When I send a POST request to "/api/channels/{channel_id}/ticket" with body:
      """
      {"ticket_url": "PROJ-123"}
      """
    Then the response status should be 400

    # An agent links the ticket through the API; the open popup follows.
    When I send a POST request to "/api/channels/{channel_id}/ticket" with body:
      """
      {"ticket_url": "https://example.atlassian.net/browse/PROJ-123"}
      """
    Then the response status should be 200
    And the row info "ticket" should read "https://example.atlassian.net/browse/PROJ-123"
    And the row info should show the channel's path and branch "main"
    # The channel header shows it by its key, as a link.
    And the element "[data-testid='header-ticket']" should contain text "PROJ-123"

    When I right-click on "bdd-row-ticket" in the sidebar
    Then the page should contain text "Open Ticket"

    # The dialog opens with the current URL to edit.
    When I click on "Edit Ticket" in the context menu
    Then I wait for "[data-testid='ticket-dialog']" to be visible
    And the field "[data-testid='ticket-input']" should hold "https://example.atlassian.net/browse/PROJ-123"

    # What the API would reject can't be saved.
    When I clear and type "PROJ-456" into "[data-testid='ticket-input']"
    Then the element "[data-testid='ticket-error']" should contain text "Must be an absolute http(s) URL"
    And the element "[data-testid='ticket-submit']:disabled" should be visible

    When I clear and type "https://github.com/o/r/issues/7" into "[data-testid='ticket-input']"
    And I click on "[data-testid='ticket-submit']"
    Then I wait up to "2s" for "[data-testid='ticket-dialog']" to disappear
    When I rest the pointer on "bdd-row-ticket" in the sidebar
    Then the row info "ticket" should read "https://github.com/o/r/issues/7"
    And the element "[data-testid='header-ticket']" should contain text "o/r#7"

    # Saving it empty clears it.
    When I right-click on "bdd-row-ticket" in the sidebar
    And I click on "Edit Ticket" in the context menu
    Then I wait for "[data-testid='ticket-dialog']" to be visible
    When I clear and type "" into "[data-testid='ticket-input']"
    And I click on "[data-testid='ticket-submit']"
    Then I wait up to "2s" for "[data-testid='ticket-dialog']" to disappear
    When I rest the pointer on "bdd-row-ticket" in the sidebar
    Then the row info should not show "ticket"
    And the element "[data-testid='header-ticket']" should not exist

  @row-tags
  Scenario: A channel's tags show in its row info, are edited from the context menu and filter the search
    Given I set up a test channel via API for git repo "bdd-row-tags"
    And I open the app in a browser
    And I wait for text "bdd-row-tags" to appear

    When I rest the pointer on "bdd-row-tags" in the sidebar
    Then the row info should not show "tags"

    # Only tags the search can match are taken.
    When I send a POST request to "/api/channels/{channel_id}/tags" with body:
      """
      {"add": ["two words"]}
      """
    Then the response status should be 400

    # An agent tags it through the API; the open popup follows.
    When I send a POST request to "/api/channels/{channel_id}/tags" with body:
      """
      {"add": ["#BDD-Web", "bdd-api"]}
      """
    Then the response status should be 200
    And the response should contain "bdd-web"
    And the row info "tags" should read "bdd-api, bdd-web"
    When I send a GET request to "/api/tags"
    Then the response should contain "bdd-web"

    # The dialog shows its tags; one is removed, another added.
    When I right-click on "bdd-row-tags" in the sidebar
    And I click on "Edit Tags" in the context menu
    Then I wait for "[data-testid='tags-dialog']" to be visible
    And the element "[data-testid='tags-chip-bdd-web']" should be visible
    When I click on "[data-testid='tags-remove-bdd-web']"
    And I type "bdd-auth" into "[data-testid='tags-input']"
    And I press Enter
    Then the element "[data-testid='tags-chip-bdd-auth']" should be visible
    When I click on "[data-testid='tags-submit']"
    Then I wait up to "2s" for "[data-testid='tags-dialog']" to disappear
    When I rest the pointer on "bdd-row-tags" in the sidebar
    Then the row info "tags" should read "bdd-api, bdd-auth"

    # The search filters by every tag:x and the text alike.
    When I type "tag:bdd-api tag:bdd-auth" into "input[placeholder='Search...']"
    Then I wait for text "bdd-row-tags" to appear
    When I clear and type "tag:bdd-api tag:bdd-web" into "input[placeholder='Search...']"
    Then I wait up to "5s" for text "bdd-row-tags" to disappear
    When I clear and type "tag:bdd-auth row-tags" into "input[placeholder='Search...']"
    Then I wait for text "bdd-row-tags" to appear
    When I clear and type "tag:bdd-auth zz-no-match" into "input[placeholder='Search...']"
    Then I wait up to "5s" for text "bdd-row-tags" to disappear
