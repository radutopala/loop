@frontend @slow @bash-shortcuts
Feature: Terminal Bash Shortcuts Journey
  End-to-end journey verifying the bash-shortcut `$` button in the terminal
  pane footer. The button mounts on `target !== "agent"` panes (Host Shell)
  and on the Docker Shell pane (which runs `/bin/bash` inside the agent
  container — `showPrompts={target === "agent" && !cmd}` resolves to false).
  The Docker Agent pane keeps the prompt `#` picker and never shows `$`,
  even when bash shortcuts are configured.

  Scenario: Host Shell pane exposes the $ bash-shortcut picker and picks a shortcut
    # Pre-seed a bash shortcut via API.
    Given I add a bash shortcut "bdd-bash-ls" with command "ls -la /work" via API

    # Set up channel and open browser.
    Given I set up a test channel via API for directory "/tmp/bdd-bash-host"
    And I open the app in a browser
    And I wait for text "bdd-bash-host" to appear
    When I click on "bdd-bash-host" in the sidebar
    Then I wait for "textarea" to be visible

    # Add the Host Shell panel — target="host" disables the prompt picker so
    # only the $ bash picker can appear when bash shortcuts exist.
    When I add a "Host Shell" panel
    Then I wait for "button[data-testid^='terminal-bash-shortcuts-btn-']" to be visible

    # Open the menu and verify the seeded shortcut is listed.
    When I click on "button[data-testid^='terminal-bash-shortcuts-btn-']"
    Then I wait for text "$bdd-bash-ls" to appear
    And the element "[data-testid^='terminal-shortcuts-menu-']" should be visible

    # Pick the shortcut — the menu closes after onMouseDown fires pickBash().
    When I click on the element with text "$bdd-bash-ls"
    Then I wait for text "$bdd-bash-ls" to disappear

  Scenario: Host Shell pane hides the $ button when no bash shortcuts exist
    # Clear any pre-existing bash shortcuts.
    Given I clear all bash shortcuts via API
    And I set up a test channel via API for directory "/tmp/bdd-bash-host-empty"
    And I open the app in a browser
    And I wait for text "bdd-bash-host-empty" to appear
    When I click on "bdd-bash-host-empty" in the sidebar
    Then I wait for "textarea" to be visible

    When I add a "Host Shell" panel
    # Allow fetchBashShortcuts to complete; with an empty list the $ button
    # never mounts (TerminalShortcuts.tsx: `showBashBtn = !promptsEnabled && bash.length > 0`).
    And I wait "2s"
    Then the element "button[data-testid^='terminal-bash-shortcuts-btn-']" should not exist

  Scenario: Docker Agent pane shows # picker and never the $ picker
    # Seed BOTH a prompt and a bash shortcut so we know each picker would
    # otherwise be enabled. Docker Agent has target="agent" without cmd, so
    # `showPrompts` resolves to true and the # picker wins exclusively
    # (TerminalShortcuts.tsx: `showPromptBtn = promptsEnabled && ...`,
    # `showBashBtn = !promptsEnabled && ...`).
    Given I add a prompt shortcut "bdd-agent-prompt" with prompt "review this" via API
    And I add a bash shortcut "bdd-agent-bash" with command "echo hi" via API
    And I set up a test channel via API for directory "/tmp/bdd-bash-agent"
    And I open the app in a browser
    And I wait for text "bdd-bash-agent" to appear
    When I click on "bdd-bash-agent" in the sidebar
    Then I wait for "textarea" to be visible

    When I add a "Docker Agent" panel
    Then I wait for "button[data-testid^='terminal-shortcuts-btn-']" to be visible
    And the element "button[data-testid^='terminal-bash-shortcuts-btn-']" should not exist
