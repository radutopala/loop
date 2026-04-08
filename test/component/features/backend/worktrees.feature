@backend
Feature: Worktrees endpoint
  The Loop API exposes endpoints for creating and deleting git worktrees.

  Scenario: Create and delete a worktree via API
    Given I set up a test channel via API for git repo "bdd-wt-del"
    And I set up a worktree "bdd-wt-del" on branch "main" under the current channel via API

    # Delete the worktree
    When I send a DELETE request to "/api/worktrees/{worktree_thread_id}"
    Then the response status should be 204

    # Verify the worktree thread is gone (second delete returns 404)
    When I send a DELETE request to "/api/worktrees/{worktree_thread_id}"
    Then the response status should be 404

  Scenario: Deleting a non-existent worktree returns not found
    When I send a DELETE request to "/api/worktrees/does-not-exist"
    Then the response status should be 404

  Scenario: Deleting a non-worktree channel returns bad request
    Given I set up a test channel via API for directory "/tmp/bdd-wt-nonwt"
    When I send a DELETE request to "/api/worktrees/{channel_id}"
    Then the response status should be 400
    And the response should contain "not a worktree"
