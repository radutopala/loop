@backend
Feature: Worktrees endpoint
  The Loop API exposes endpoints for creating and removing git worktrees.

  Scenario: Create and remove a worktree via API
    Given I set up a test channel via API for git repo "bdd-wt-del"
    And I set up a worktree "bdd-wt-del" on branch "main" under the current channel via API

    # Remove the worktree (disk + thread)
    When I send a DELETE request to "/api/worktrees" with body:
      """
      {"channel_id": "{channel_id}", "worktree_path": "{worktree_path}", "thread_id": "{worktree_thread_id}"}
      """
    Then the response status should be 204

    # Verify the worktree is gone — removing again fails (worktree no longer on disk)
    When I send a DELETE request to "/api/worktrees" with body:
      """
      {"channel_id": "{channel_id}", "worktree_path": "{worktree_path}"}
      """
    Then the response status should be 500

  Scenario: Fork a worktree thread via API
    Given I set up a test channel via API for git repo "bdd-wt-fork"
    And I set up a worktree "bdd-wt-fork" on branch "main" under the current channel via API

    # Forking a worktree thread branches a fresh worktree off the source's
    # committed branch and carries the session over — one polymorphic endpoint
    # detects the worktree source and returns its path.
    When I send a POST request to "/api/threads/{worktree_thread_id}/fork" with body:
      """
      {}
      """
    Then the response status should be 201
    And the response JSON "thread_id" should not be empty
    And the response JSON "worktree_path" should not be empty

  Scenario: Removing with missing fields returns bad request
    When I send a DELETE request to "/api/worktrees" with body:
      """
      {"channel_id": ""}
      """
    Then the response status should be 400
    And the response should contain "channel_id and worktree_path required"

  Scenario: Removing with unknown channel returns bad request
    When I send a DELETE request to "/api/worktrees" with body:
      """
      {"channel_id": "does-not-exist", "worktree_path": "/tmp/fake"}
      """
    Then the response status should be 400
    And the response should contain "channel not found"
