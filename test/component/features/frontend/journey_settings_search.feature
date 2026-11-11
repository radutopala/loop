@frontend @settings-search
Feature: Settings Search Journey
  The search box above the settings sidebar filters its sections by name
  and by the keys inside them, parent keys included, and lists the matched
  keys under each section; clicking one opens its section on that field.

  Scenario: Searching a parent key finds the keys inside it
    Given I open the app in a browser
    Then I wait for text "Settings" to appear

    When I open the settings panel and select "Daemon"
    And I type "dictation" into "[data-testid='settings-search']"
    Then I wait for "[data-testid='settings-search-hit-dictation.model']" to be visible
    And the element "[data-testid='settings-search-hit-dictation.idle_stop_sec']" should be visible
    And the element "[data-testid='settings-nav-dictation']" should be visible
    And the element "[data-testid='settings-nav-daemon']" should not exist

    When I clear and type "dictation idle" into "[data-testid='settings-search']"
    Then I wait for "[data-testid='settings-search-hit-dictation.idle_stop_sec']" to be visible
    And the element "[data-testid='settings-search-hit-dictation.model']" should not exist

    When I click on "[data-testid='settings-search-hit-dictation.idle_stop_sec']"
    Then I wait for "[data-config-key='dictation.idle_stop_sec']" to be visible

    When I clear and type "no such setting zz" into "[data-testid='settings-search']"
    Then I wait for "[data-testid='settings-search-empty']" to be visible
