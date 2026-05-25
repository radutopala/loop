@frontend @slow @settings-restore-builtins
Feature: Settings Restore Built-ins Journey
  End-to-end journey verifying the "Restore built-ins" bar in Settings
  re-adds missing seeded shortcuts/workflows and reports already-present
  ones without duplicating them.

  Scenario: Restore built-in workflows reports them as already present
    # Default fixture state: fsmigrate seeded "review-loop" and
    # "review-fix-loop" on daemon startup. Restoring should add nothing
    # and skip both names.
    Given I open the app in a browser
    Then I wait for text "Settings" to appear

    # Single combined step retry-clicks the sidebar Settings button (React
    # hydration can race a single click), waits for the panel + loaded=true
    # to render the named NavButton, then clicks it.
    When I open the settings panel and select "Workflows"
    Then I wait for text "Restore built-in workflows" to appear

    When I click button "Restore built-in workflows" in the settings panel
    Then I wait for text "Already present:" to appear
    And the page should contain text "review-loop"
    And the page should contain text "review-fix-loop"

  Scenario: Restore built-in shortcuts re-adds after clearing via API
    # Clear all shortcuts (including the fsmigrate-seeded "builtin code
    # review") so the Restore button has something to re-add.
    Given I clear all prompt shortcuts via API
    And I open the app in a browser
    And I wait for text "Settings" to appear

    # See workflows scenario above for the why behind this combined step.
    When I open the settings panel and select "Prompt Shortcuts"
    Then I wait for text "Restore built-in shortcuts" to appear

    When I click button "Restore built-in shortcuts" in the settings panel
    Then I wait for text "Added:" to appear
    And the page should contain text "builtin code review"

    # Idempotency: a second click should now report it as already present.
    When I click button "Restore built-in shortcuts" in the settings panel
    Then I wait for text "Already present:" to appear
