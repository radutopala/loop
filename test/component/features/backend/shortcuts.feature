@backend
Feature: Prompt Shortcuts endpoint
  The Loop API exposes shortcuts endpoints for listing and managing prompt shortcuts.

  Scenario: Shortcuts returns empty array when no shortcuts configured
    # Clear any built-in shortcuts (e.g. the "builtin code review" seeded
    # by fsmigrate) so the empty-list assertion holds.
    Given I clear all prompt shortcuts via API
    When I send a GET request to "/api/shortcuts"
    Then the response status should be 200
    And the response should contain "[]"

  Scenario: Add, list, update, and delete a shortcut via API
    # Clear built-in shortcuts so the final "[]" assertion holds.
    Given I clear all prompt shortcuts via API
    # Add a shortcut
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "add", "name": "bdd-lint", "description": "Run linter", "prompt": "Run make lint"}
      """
    Then the response status should be 204

    # List and verify it appears
    When I send a GET request to "/api/shortcuts"
    Then the response status should be 200
    And the response should contain "bdd-lint"
    And the response should contain "Run make lint"

    # Update the shortcut
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "update", "name": "bdd-lint", "description": "Lint with fix", "prompt": "Run make lint --fix"}
      """
    Then the response status should be 204

    # Verify the update
    When I send a GET request to "/api/shortcuts"
    Then the response status should be 200
    And the response should contain "make lint --fix"

    # Delete the shortcut
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "delete", "name": "bdd-lint"}
      """
    Then the response status should be 204

    # Verify it's gone
    When I send a GET request to "/api/shortcuts"
    Then the response status should be 200
    And the response should contain "[]"

  Scenario: Adding a duplicate shortcut returns conflict
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "add", "name": "bdd-dup", "prompt": "first"}
      """
    Then the response status should be 204

    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "add", "name": "bdd-dup", "prompt": "second"}
      """
    Then the response status should be 409

    # Clean up
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "delete", "name": "bdd-dup"}
      """
    Then the response status should be 204

  Scenario: Deleting a non-existent shortcut returns not found
    When I send a POST request to "/api/shortcuts" with body:
      """
      {"action": "delete", "name": "does-not-exist"}
      """
    Then the response status should be 404
