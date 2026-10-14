@backend @backup
Feature: Backup
  Loop zips its database and playgrounds into a directory on a schedule,
  or when asked, encrypted to a key it creates, and prunes the backups no
  retention tier keeps.

  @slow
  Scenario: Scheduled and on-demand backups are written and pruned
    Given there is no "bdd-backups" dir in the global loop dir
    And I write "<h1>bdd</h1>" to the global loop file "playground/bdd-backup/index.html" outside Loop

    # Nothing is written until backups are enabled.
    When I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false, "keep_last": 1, "keep_daily": 0, "keep_weekly": 0, "keep_monthly": 0}
      """
    And I send a GET request to "/api/backup"
    Then the response status should be 200
    And the response JSON "enabled" should be "false"
    And the response JSON "count" should be "0"
    And the response JSON "times_per_day" should be "1"
    And the response JSON "keep_last" should be "1"
    And the response JSON "keep_daily" should be "0"

    # Once enabled, the first backup is due at once.
    When I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": true, "keep_last": 1, "keep_daily": 0, "keep_weekly": 0, "keep_monthly": 0}
      """
    Then 1 backup should be written to "bdd-backups" on schedule
    And the newest backup in "bdd-backups" should be a plain zip holding "playground/global/bdd-backup/index.html"
    And the newest backup in "bdd-backups" should be a plain zip holding "manifest.json"
    When I send a GET request to "/api/backup"
    Then the response status should be 200
    And the response JSON "count" should be "1"
    And the response JSON "total_size" should not be empty
    And the response JSON "next_at" should not be empty
    And the response's last backup should be the newest in "bdd-backups"

    # Backing up now replaces it, as only one is kept.
    Given the second of the newest backup in "bdd-backups" has passed
    When I send a POST request to "/api/backup" with body:
      """
      {}
      """
    Then the response status should be 202
    And 1 backup should be written to "bdd-backups"
    When I send a GET request to "/api/backup"
    Then the response status should be 200
    And the response JSON "count" should be "1"
    And the response's last backup should be the newest in "bdd-backups"

    # Turned off again, so later scenarios don't back up.
    When I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false}
      """
    Then the response status should be 204

  Scenario: Backups are encrypted to the key Loop creates
    Given there is no "bdd-backups" dir in the global loop dir
    And I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false, "keep_last": 2}
      """
    When I send a POST request to "/api/backup" with body:
      """
      {}
      """
    Then the response status should be 202
    And 1 backup should be written to "bdd-backups"
    And the newest backup in "bdd-backups" should be a plain zip holding "loop.db"

    # The key's public half goes into the config; new backups are encrypted.
    When I create a backup key
    And I send a GET request to "/api/backup"
    Then the response JSON "recipient" should not be empty
    Given the second of the newest backup in "bdd-backups" has passed
    When I send a POST request to "/api/backup" with body:
      """
      {}
      """
    Then the response status should be 202
    And 2 backups should be written to "bdd-backups"
    And the newest backup in "bdd-backups" should be an encrypted zip holding "loop.db"

    # A key that isn't an age public key fails the backup.
    When I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false, "recipient": "not-a-key"}
      """
    And I send a POST request to "/api/backup" with body:
      """
      {}
      """
    Then the response status should be 400

    When I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false}
      """
    Then the response status should be 204
