@frontend @slow
Feature: Terminal Prompt Shortcuts Journey
  End-to-end journey verifying the prompt-shortcut `#` button portaled into
  the Docker Agent terminal pane header opens a menu of shortcuts and that
  picking one closes the menu (after pasting the prompt as bracketed paste
  into the terminal).

  Scenario: Terminal pane exposes shortcut menu and picks a shortcut
    # Pre-seed a shortcut via API
    Given I add a prompt shortcut "bdd-term-lint" with prompt "Run make lint and report results" via API

    # Set up channel and open browser
    Given I set up a test channel via API for directory "/tmp/bdd-term-shortcuts"
    And I open the app in a browser
    And I wait for text "bdd-term-shortcuts" to appear
    When I click on "bdd-term-shortcuts" in the sidebar
    Then I wait for "textarea" to be visible

    # Add the Docker Agent panel — this is the only target that renders the
    # terminal-shortcuts `#` button in its pane header (target === "agent"
    # with hideActions in WorkspaceLayout).
    When I add a "Docker Agent" panel
    Then I wait for "button[data-testid^='terminal-shortcuts-btn-']" to be visible

    # Open the menu and verify the seeded shortcut is listed.
    # We use the data-testid selector because the chat input also has a
    # button with title="Prompt shortcuts" and would be ambiguous here.
    When I click on "button[data-testid^='terminal-shortcuts-btn-']"
    Then I wait for text "#bdd-term-lint" to appear
    # Menu portal mounted — verify the actual menu element is present.
    # (The prompt text isn't rendered anywhere in the picker — only name and
    # description — so asserting the prompt would always fail.)
    And the element "[data-testid^='terminal-shortcuts-menu-']" should be visible

    # Pick the shortcut — the menu closes after onMouseDown fires pick().
    When I click on the element with text "#bdd-term-lint"
    Then I wait for text "#bdd-term-lint" to disappear

  Scenario: Terminal pane hides the shortcut button when no shortcuts exist
    # No shortcut seeding — TerminalShortcuts returns null when the
    # fetched list is empty (TerminalShortcuts.tsx: `shortcuts.length === 0`).
    Given I set up a test channel via API for directory "/tmp/bdd-term-no-shortcuts"
    And I open the app in a browser
    And I wait for text "bdd-term-no-shortcuts" to appear
    When I click on "bdd-term-no-shortcuts" in the sidebar
    Then I wait for "textarea" to be visible

    When I add a "Docker Agent" panel
    # Allow fetchShortcuts to complete; with an empty list the # button
    # never mounts. 2s is well above the local API roundtrip.
    And I wait "2s"
    Then the element "button[data-testid^='terminal-shortcuts-btn-']" should not exist

  Scenario: Host Shell terminal does not expose the shortcut button
    # Seed a shortcut so we know the picker would otherwise be enabled.
    # The Host Shell pane uses target="host", and Terminal.tsx only
    # mounts TerminalShortcuts when `target === "agent"`.
    Given I add a prompt shortcut "bdd-term-host-gate" with prompt "Run make lint" via API
    And I set up a test channel via API for directory "/tmp/bdd-term-host-shell"
    And I open the app in a browser
    And I wait for text "bdd-term-host-shell" to appear
    When I click on "bdd-term-host-shell" in the sidebar
    Then I wait for "textarea" to be visible

    When I add a "Host Shell" panel
    And I wait "2s"
    Then the element "button[data-testid^='terminal-shortcuts-btn-']" should not exist
