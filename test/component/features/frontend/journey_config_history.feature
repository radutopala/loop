@frontend @slow @config-history
Feature: Config History Journey
  End-to-end journey through Settings > History: picking a revision shows
  its diff, "Diff against" compares it with another revision, and restoring
  one writes it back.

  Scenario: Compare two revisions and restore one
    Given I remember the newest global config revision as "before"
    And I save the global config via API with the line "// bdd journey first" appended
    And I remember the newest global config revision as "first"
    And I save the global config via API with the line "// bdd journey second" appended
    And I remember the newest global config revision as "second"
    And I open the app in a browser
    And I wait for text "Settings" to appear

    When I open the settings panel and select "History"
    Then I wait for text "Global Config History" to appear

    # A revision shows what it changed from the one before it.
    When I click config history revision "second"
    Then the element "[data-testid='config-history-diff-caption']" should contain text "Changes from #{rev:first} to #{rev:second}"
    And the element "[data-testid='config-history-diff']" should contain text "+// bdd journey second"
    # The diff fills the panel instead of a fixed short box.
    And I wait for "[data-testid='config-history-diff']" to be at least 400px tall

    # Diff against another revision.
    When I select "{rev:before}" from "[data-testid='config-history-against']"
    Then the element "[data-testid='config-history-diff-caption']" should contain text "Changes from #{rev:before} to #{rev:second}"
    And the element "[data-testid='config-history-diff']" should contain text "+// bdd journey first"
    And the element "[data-testid='config-history-diff']" should contain text "+// bdd journey second"

    # Restore the revision from before the journey.
    When I click config history revision "before"
    And I click button "Restore this version" in the settings panel
    Then the element "[data-testid='config-history-item']" should contain text "Restored #{rev:before}"
    And the global config history should start with sources "restore:{rev:before}"
    And a GET to "/api/config" should soon not contain "bdd journey"
