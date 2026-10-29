@frontend @pprof
Feature: Settings Profiling Journey
  Settings has a Profiling section for the daemon's pprof listener.

  Scenario: The Profiling section shows the pprof settings
    Given I open the app in a browser
    And I wait for text "Settings" to appear

    When I open the settings panel and select "Profiling"
    Then I wait for text "Serve the daemon's Go pprof profiles" to appear
    And I wait for text "The host:port the pprof listener binds" to appear
