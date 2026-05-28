@frontend @docs
Feature: Documentation screenshots
  Captures screenshots of key UI surfaces for the documentation site.
  Tagged @docs so normal BDD runs skip it (GODOG_TAGS defaults to ~@docs);
  run via `make docs-capture`, which also sets LOOP_DOCS_CAPTURE so the
  capture step writes PNGs into docs/static/images/features.

  Scenario: Capture the workflows panel
    Given I open the app in a browser
    And I wait for text "Settings" to appear

    When I open the workflows panel
    And I wait up to "10s" for text "+ Run" to appear
    Then I capture screenshot "workflows-panel"
