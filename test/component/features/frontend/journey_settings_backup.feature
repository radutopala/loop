@frontend @slow @backup
Feature: Settings Backup Journey
  The Backup section of Settings shows the backups, writes one on demand
  and creates the key they're encrypted with.

  Scenario: Back up now and create a key from Settings
    Given there is no "bdd-backups" dir in the global loop dir
    And I set the global backup config to the "bdd-backups" dir with:
      """
      {"enabled": false, "keep_last": 2}
      """
    And I open the app in a browser
    And I wait for text "Settings" to appear

    When I open the settings panel and select "Backup"
    Then I wait for text "No backups in" to appear
    And the element "[data-testid='backup-summary']" should contain text "Scheduled backups are off"
    And the element "[data-testid='backup-encryption']" should contain text "Not encrypted"

    When I click button "Back up now" in the settings panel
    Then I wait for text "Last backup" to appear
    And the element "[data-testid='backup-summary']" should contain text "1 kept in"
    And the element "[data-testid='backup-summary']" should contain text "in all)"
    And 1 backup should be written to "bdd-backups"

    # The private key is shown once; the public key goes into the config.
    When I click button "Create key" in the settings panel
    Then I wait for text "Save this private key" to appear
    And the element "[data-testid='backup-private-key-value']" should contain text "AGE-SECRET-KEY-1"
    And the element "[data-testid='backup-encryption']" should contain text "Encrypted to the public key age1"
    When I click button "I saved it" in the settings panel
    Then I wait for text "Save this private key" to disappear
    And the element "[data-testid='backup-create-key']" should contain text "Create a new key"

    When I remove the global backup config
    Then the response status should be 204
