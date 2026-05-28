@frontend @docs
Feature: Documentation walkthrough
  Captures the documentation assets in a single end-to-end journey: one browser
  session is screen-recorded the whole way through (muxed to an MP4 for manual
  upload) while still-screenshots are captured at key moments. The recording is a
  guided tour — a fading "∞ Loop" title card opens and closes it, each panel is
  held for a few seconds, then a caption explains it (the MP4 preserves real-time
  pacing so the pauses are real and easy to follow).
  Tagged @docs so normal BDD runs skip it (GODOG_TAGS defaults to ~@docs);
  run via `make docs-capture`, which sets LOOP_DOCS_CAPTURE so the capture steps
  write assets (screenshots into docs/static/images/features, the MP4 into the
  gitignored docs/videos).

  # The journey runs against the sample "acme-notes" project (real files, git
  # history, uncommitted edits, seeded Kanban tickets, a scheduled task, a prompt
  # shortcut, and a bash shortcut). One continuous browser session = one unbroken
  # recording. Captions are hidden before every screenshot so they never obscure
  # the panel; each panel is paused on first, then captioned.
  Background:
    Given I set up a sample project channel
    And I open the app in a browser
    And I wait for text "acme-notes" to appear
    And I click on "acme-notes" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: End-to-end product walkthrough
    When I start recording
    # Branded intro — fades in, holds, fades out (the step drives the hold)
    And I show the Loop title card
    And I hide caption
    # Chat — live agent reply (pause on it, then caption)
    And I type "In one sentence, what can you help me build with this notes service?" into "textarea"
    And I press Enter
    And I wait for "[data-msg-id]:not([data-is-user])" to be visible
    And I wait "5s"
    And I capture screenshot "chat-conversation"
    And I show caption "Chat — describe what you want; the agent works in a sandboxed container"
    And I wait "4s"
    And I hide caption
    # Prompt shortcuts — open the picker, then actually run one
    And I type "#" into "textarea"
    And I wait for text "review-diff" to appear
    And I wait "4s"
    And I capture screenshot "chat-shortcut"
    And I type "review-diff" into "textarea"
    And I wait "2s"
    And I press Enter
    And I wait for text "Review the uncommitted changes" to appear
    And I wait "8s"
    And I show caption "Prompt shortcuts — type # to pick a reusable prompt; it runs instantly"
    And I wait "4s"
    And I hide caption
    # Editor
    And I click on "[data-testid='layout-tab-Editor']"
    And I wait "4s"
    And I capture screenshot "editor"
    And I show caption "Editor — file tree, code editor, a shell, and chat side by side"
    And I wait "4s"
    And I hide caption
    # Memory
    And I click on "[data-testid='layout-tab-Memory']"
    And I wait "4s"
    And I show caption "Memory — searchable long-term memory the agent can recall"
    And I wait "4s"
    And I hide caption
    # Terminal
    And I click on "[data-testid='layout-tab-Terminal']"
    And I wait "4s"
    And I show caption "Terminal — host and container shells, with command shortcuts"
    And I wait "4s"
    And I hide caption
    # Git
    And I click on "[data-testid='layout-tab-Git']"
    And I wait "4s"
    And I show caption "Git — review the diff, branches, commits, and worktrees"
    And I wait "4s"
    And I hide caption
    # Browser
    And I click on "[data-testid='layout-tab-Browser Chat']"
    And I wait "4s"
    And I show caption "Browser — drive a real Chrome side-by-side with chat"
    And I wait "4s"
    And I hide caption
    # Sessions
    And I click on "[data-testid='layout-tab-Sessions']"
    And I wait "4s"
    And I show caption "Sessions — browse and resume past Claude sessions"
    And I wait "4s"
    And I hide caption
    # Swarm
    And I click on "[data-testid='layout-tab-Swarm']"
    And I wait "4s"
    And I show caption "Swarm — run several agents in parallel"
    And I wait "4s"
    And I hide caption
    # Canvas
    And I click on "[data-testid='layout-tab-Canvas']"
    And I wait "4s"
    And I show caption "Canvas — arrange panels freely on an infinite canvas"
    And I wait "4s"
    And I hide caption
    # Playground
    And I click on "[data-testid='layout-tab-Playground']"
    And I wait "4s"
    And I show caption "Playground — a live HTML/CSS/JS sandbox"
    And I wait "4s"
    And I hide caption
    # Kanban
    And I click on "[data-testid='layout-tab-Kanban']"
    And I wait "4s"
    And I capture screenshot "kanban-board"
    And I show caption "Kanban — track tickets across Open, In Progress, and Closed"
    And I wait "4s"
    And I hide caption
    # Workflows tab
    And I click on "[data-testid='layout-tab-Workflows']"
    And I wait "4s"
    And I show caption "Workflows — declarative multi-step pipelines with a live DAG"
    And I wait "4s"
    And I hide caption
    # Multi-panel — compose a custom workspace
    And I click on "[data-testid='layout-tab-Chat']"
    And I add a "Files" panel
    And I add a "Git" panel
    And I wait "4s"
    And I capture screenshot "multi-panel-workspace"
    And I show caption "Compose your own workspace — split any panels together"
    And I wait "4s"
    And I hide caption
    # Scheduled tasks (sidebar overlay)
    And I open the global tasks panel
    And I wait "4s"
    And I capture screenshot "tasks-panel"
    And I show caption "Scheduled tasks — run prompts on a cron or interval"
    And I wait "4s"
    And I hide caption
    # Workflows overlay
    And I open the workflows panel
    And I wait up to "10s" for text "+ Run" to appear
    And I wait "3s"
    And I capture screenshot "workflows-panel"
    # Settings
    And I open the settings panel and select "Workflows"
    And I wait "4s"
    And I capture screenshot "settings-panel"
    And I show caption "Settings — configure everything, with a form or raw JSON"
    And I wait "4s"
    And I hide caption
    # Branded outro (the step drives the hold)
    And I show the Loop title card
    And I hide caption
    Then I stop recording "journey"
