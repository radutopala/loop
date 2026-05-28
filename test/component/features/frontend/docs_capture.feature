@frontend @docs
Feature: Documentation walkthrough
  Captures the documentation assets in a single end-to-end journey: one browser
  session is screen-recorded the whole way through (muxed to an MP4 for manual
  upload) while still-screenshots are captured at key moments along the way.
  Tagged @docs so normal BDD runs skip it (GODOG_TAGS defaults to ~@docs);
  run via `make docs-capture`, which sets LOOP_DOCS_CAPTURE so the capture steps
  write assets (screenshots into docs/static/images/features, the MP4 into the
  gitignored docs/videos).

  # The journey runs against the sample "acme-notes" project (real files, git
  # history, uncommitted edits) so the docs assets show a realistic workspace.
  Background:
    Given I set up a sample project channel
    And I open the app in a browser
    And I wait for text "acme-notes" to appear
    And I click on "acme-notes" in the sidebar
    And I wait for "textarea" to be visible

  # One continuous browser session = one unbroken recording. Screenshots are
  # captured before each layout mutation, so chat-conversation shows the default
  # (chat + git) Chat layout and multi-panel-workspace shows the added panels.
  # The live Claude agent reply needs the docs-capture auth injection + non-root
  # agent uid (scripts/test-component.sh); otherwise the agent can't run.
  Scenario: End-to-end product walkthrough
    When I start recording
    And I type "In one sentence, what can you help me build with this notes service?" into "textarea"
    And I press Enter
    And I wait for "[data-msg-id]:not([data-is-user])" to be visible
    And I wait "4s"
    And I capture screenshot "chat-conversation"
    And I add a "Files" panel
    And I add a "Git" panel
    And I wait "2s"
    And I capture screenshot "multi-panel-workspace"
    And I open the workflows panel
    And I wait up to "10s" for text "+ Run" to appear
    And I capture screenshot "workflows-panel"
    And I wait "1s"
    Then I stop recording "journey"
