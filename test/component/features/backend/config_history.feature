@backend @config-history
Feature: Config history
  Loop records a revision of the global config each time its content
  changes, whether Loop or another editor wrote it, and serves each
  revision's diff from the one before it or from another revision, and
  restores one.

  Scenario: Saves and outside edits are recorded, diffed and restored
    Given I remember the newest global config revision as "before"

    # A save through Settings.
    When I save the global config via API with the line "// bdd settings save" appended
    Then the global config history should start with sources "settings"
    And I remember the newest global config revision as "saved"
    When I send a GET request to "/api/config/history/{rev:saved}"
    Then the response status should be 200
    And the response diff should contain "+// bdd settings save"

    # An edit outside Loop is recorded before Loop's next write.
    When I append the line "// bdd outside edit" to the global config file outside Loop
    And I save the global config via API with the line "// bdd second save" appended
    Then the global config history should start with sources "settings, external"
    And I remember the newest global config revision as "latest"

    # Diffing against another revision spans every change in between.
    When I send a GET request to "/api/config/history/{rev:latest}?against={rev:before}"
    Then the response status should be 200
    And the response diff should contain "+// bdd settings save"
    And the response diff should contain "+// bdd outside edit"
    And the response diff should contain "+// bdd second save"

    # Against the latest, the oldest of them shows what a restore would undo.
    When I send a GET request to "/api/config/history/{rev:before}?against={rev:latest}"
    Then the response status should be 200
    And the response diff should contain "-// bdd outside edit"
    And the response diff should not contain "+// bdd"

    # Restoring writes the old content back as a new revision.
    When I send a POST request to "/api/config/history/{rev:before}/restore" with body:
      """
      {}
      """
    Then the response status should be 204
    And the global config history should start with sources "restore:{rev:before}, settings, external"
    And a GET to "/api/config" should soon not contain "// bdd"

  Scenario: Unknown revisions and bad ids are rejected
    When I send a GET request to "/api/config/history/999999999"
    Then the response status should be 404
    When I send a GET request to "/api/config/history/abc"
    Then the response status should be 400
    Given I remember the newest global config revision as "current"
    When I send a GET request to "/api/config/history/{rev:current}?against=abc"
    Then the response status should be 400
    When I send a GET request to "/api/config/history/{rev:current}?against=999999999"
    Then the response status should be 404
    When I send a POST request to "/api/config/history/999999999/restore" with body:
      """
      {}
      """
    Then the response status should be 404
