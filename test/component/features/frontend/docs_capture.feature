@frontend @docs
Feature: Documentation walkthrough
  Captures the documentation assets in a single end-to-end journey: one browser
  session is screen-recorded the whole way through (muxed to an MP4 for manual
  upload) while still-screenshots are captured at key moments. The recording is a
  guided tour — a fading "∞ Loop" title card opens and closes it; for each stop a
  caption is shown first to explain what's coming, then (after a beat) the action
  happens. The MP4 preserves real-time pacing so every pause is real and easy to
  follow.
  Tagged @docs so normal BDD runs skip it (GODOG_TAGS defaults to ~@docs);
  run via `make docs-capture`, which sets LOOP_DOCS_CAPTURE so the capture steps
  write assets (screenshots into docs/static/images/features, the MP4 into the
  gitignored docs/videos).

  # The journey runs against the sample "acme-notes" project (real files, git
  # history, uncommitted edits, seeded Kanban tickets, a scheduled task, a prompt
  # shortcut, and a bash shortcut). One continuous browser session = one unbroken
  # recording. Rhythm per stop: pause -> caption (explains the next action) ->
  # pause -> action -> settle. Captions are hidden before the action so they
  # never obscure the panel or a screenshot.
  Background:
    Given I set up a sample project channel
    And I open the app in a browser
    And I wait for text "acme-notes" to appear
    And I click on "acme-notes" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: End-to-end product walkthrough
    # Inject the branded card BEFORE recording so the video opens on it
    Given I show the Loop intro card
    When I start recording
    And I show the mouse cursor
    # Branded intro — already on screen from frame 1; hold, then fade out to the app
    And I wait "3s"
    And I fade out the Loop title card
    And I hide caption
    # Chat — live agent reply
    And I wait "2s"
    And I show caption "Chat — describe what you want; the agent works in a sandboxed container"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "In one sentence, what can you help me build with this notes service?" into "textarea"
    And I press Enter
    And I wait for "[data-msg-id]:not([data-is-user])" to be visible
    And I wait up to "90s" for "button[title='Stop']" to disappear
    And I wait "4s"
    And I capture screenshot "chat-conversation"
    # Security gate — a synthetic approval card (same component the real gate
    # mounts when a risky operation pauses for the operator's decision).
    And I wait "2s"
    And I show caption "Security gate — risky operations pause for your approval"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I inject a gate.approval_requested event with req_id "docs-gate", source "chat", and target "/root/.ssh/authorized_keys"
    And I wait for text "/root/.ssh/authorized_keys" to appear
    And I wait "3s"
    And I capture screenshot "gate-approval"
    And I inject a gate.approval_resolved event with req_id "docs-gate"
    And I wait for text "/root/.ssh/authorized_keys" to disappear
    And I wait "2s"
    # Prompt shortcuts — two ways to open the picker. First: click the # button.
    And I wait "2s"
    And I show caption "Prompt shortcuts — click the # button to pick a reusable prompt"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "button[title='Prompt shortcuts']"
    And I wait for text "review-diff" to appear
    And I wait "3s"
    And I capture screenshot "chat-shortcut"
    And I press Escape
    And I wait "2s"
    # ...or just type # — same picker — then run it instantly
    And I show caption "...or just type # — then pick one and it runs instantly"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "#" into "textarea"
    And I wait for text "review-diff" to appear
    And I wait "2s"
    And I type "review-diff" into "textarea"
    And I wait "2s"
    And I press Enter
    And I wait for text "Review the uncommitted changes" to appear
    # Let the shortcut's run start (Stop button mounts), then wait for it to
    # finish and the result to render before moving on. The first run was already
    # awaited above, so there's no queuing race here.
    And I wait "2s"
    And I wait up to "90s" for "button[title='Stop']" to disappear
    And I wait "4s"
    # Git workflow (in the Chat layout, Git panel alongside) — ask the agent to
    # change a file, inspect the uncommitted diff, commit it, then review history.
    And I wait "2s"
    And I show caption "Ask the agent to change a file — it edits code in the sandbox"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Add a GET /healthz route to src/index.ts that returns { ok: true }." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    # Uncommitted Diff — the Git panel shows exactly what the agent changed
    And I wait "2s"
    And I show caption "The Git panel shows the uncommitted diff — exactly what changed"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click "Uncommitted Diff" in the git panel
    And I wait "4s"
    # Commit it — just ask in chat
    And I wait "2s"
    And I show caption "Then ask the agent to commit it"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Commit that change with a concise message." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    # Commits — the new commit lands in history
    And I wait "2s"
    And I show caption "The new commit lands in Commits"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click "Commits" in the git panel
    And I wait "4s"
    # Branches Diff — compare your branch against another
    And I wait "2s"
    And I show caption "Branches Diff compares your branch against another"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click "Branches Diff" in the git panel
    And I wait "4s"
    # Editor — open a file, edit it inline, then ask the agent to commit it
    And I wait "2s"
    And I show caption "Editor — file tree, code editor, a shell, and chat side by side"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Editor']"
    And I wait "3s"
    # Open a file from the tree — it loads into the CodeMirror editor
    And I wait "2s"
    And I show caption "Click a file in the tree to open it in the editor"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the file "README.md" in the editor tree
    And I wait for text "documentation screenshots" to appear
    And I wait "3s"
    And I capture screenshot "editor"
    # Edit it inline, then save to disk
    And I wait "2s"
    And I show caption "Edit it inline in a full code editor, then save"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I append "- PATCH /notes/:id - update a note" to the code editor
    And I wait "2s"
    And I save the editor
    And I wait "3s"
    # Ask the agent in chat to commit the change
    And I wait "2s"
    And I show caption "Then just ask the agent to commit it — same repo, same workspace"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Commit my change to README.md with a concise message." into "textarea[placeholder*='Ask Loop anything']"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    # Memory
    And I wait "2s"
    And I show caption "Memory — searchable long-term memory the agent can recall"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Memory']"
    And I wait "3s"
    # Terminal — open a Docker shell, then open the $ picker and run a saved command
    And I wait "2s"
    And I show caption "Terminal — open a shell in the container, then click $ to run a saved command"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Terminal']"
    And I wait "2s"
    And I click on the button with text "Docker Shell"
    And I wait for "button[data-testid^='terminal-bash-shortcuts-btn-']" to be visible
    And I wait "3s"
    # Open the $ picker and hold so the popup is clearly recorded
    And I click on "button[data-testid^='terminal-bash-shortcuts-btn-']"
    And I wait for text "$run-tests" to appear
    And I wait "5s"
    And I capture screenshot "docker-shell"
    And I wait "3s"
    # Select a saved command — pause on the open menu, then it runs in the shell
    And I click on the element with text "$run-tests"
    And I wait "6s"
    # Docker Agent (Resume) — split an agent terminal in below the shell; it
    # resumes THIS channel's Claude session right inside the container, so you
    # can talk to the same agent straight from a terminal.
    And I wait "2s"
    And I show caption "Open a Docker Agent terminal — it resumes this channel's session in the container"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    # Open the pane's panel selector and let it linger so the menu is recorded
    And I open the add-panel menu
    And I wait "5s"
    # Pick "Docker Agent (Resume)" — splits an agent terminal in below the shell
    And I add the "Docker Agent (Resume)" panel below in the menu
    And I wait up to "40s" for "[data-testid='docker-agent-pane'] .xterm" to be visible, best effort
    # Give the Claude TUI time to boot and resume the session inside the container
    And I wait "35s"
    # Type a message straight into the resumed agent — same session, same workspace
    And I wait "2s"
    And I show caption "Type right in the terminal — same agent, same session, no context lost"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "In one short sentence, what does this notes service do?" into the agent terminal
    And I wait "2s"
    And I submit the agent terminal
    And I wait "30s"
    And I capture screenshot "docker-agent-terminal"
    And I wait "3s"
    # Git
    And I wait "2s"
    And I show caption "Git — review the diff, branches, commits, and worktrees"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Git']"
    And I wait "3s"
    And I capture screenshot "git-panel"
    # Browser
    And I wait "2s"
    And I show caption "Browser — drive a real Chrome side-by-side with chat"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Browser Chat']"
    And I wait "3s"
    # Sessions
    And I wait "2s"
    And I show caption "Sessions — browse and resume past Claude sessions"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Sessions']"
    And I wait "3s"
    And I capture screenshot "sessions"
    # Swarm
    And I wait "2s"
    And I show caption "Swarm — run several agents in parallel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Swarm']"
    And I wait "3s"
    # Canvas
    And I wait "2s"
    And I show caption "Canvas — arrange panels freely on an infinite canvas"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Canvas']"
    And I wait "3s"
    # Playground — ask the agent to build a live HTML/CSS/JS sandbox via MCP
    And I wait "2s"
    And I show caption "Playground — ask the agent to build a live HTML/CSS/JS sandbox"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Playground']"
    And I wait "3s"
    And I type "Create a quick playground scoped to this project: a centered card that says 'Hello from Loop' with a gentle pulsing glow animation." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    # Select the playground so it renders, then wait until its iframe is live
    And I select the created playground
    And I wait up to "30s" for "[data-testid='playground-panel'] iframe" to be visible, best effort
    And I wait "4s"
    And I capture screenshot "playground"
    # Kanban
    And I wait "2s"
    And I show caption "Kanban — track tickets across Open, In Progress, and Closed"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Kanban']"
    And I wait "3s"
    And I capture screenshot "kanban-board"
    # Workflows tab
    And I wait "2s"
    And I show caption "Workflows — declarative multi-step pipelines with a live DAG"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Workflows']"
    And I wait "3s"
    # Review — the tab is present because the sample project sets review.enabled
    And I wait "2s"
    And I show caption "Review — an agent pass over your diff, with comments pushed back inline"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Review']"
    And I wait "3s"
    And I capture screenshot "review-panel"
    # Quality — split the panel into the Chat layout, run a scan, show the signal
    And I wait "2s"
    And I show caption "Quality — one architectural signal, with a treemap of hotspots"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Chat']"
    And I wait "2s"
    And I add a "Quality" panel
    And I wait "2s"
    And I click on the button with text "Scan now"
    And I wait up to "60s" for text "Modularity" to appear
    And I wait "3s"
    And I capture screenshot "quality-panel"
    # Multi-panel — compose a custom workspace: split a Host Shell under the Git panel
    And I wait "2s"
    And I show caption "Compose your own workspace — split any panel into the layout"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Chat']"
    And I wait "3s"
    # Open the Git panel's "+" selector and let it linger so the menu is recorded
    And I open the add-panel menu in the git panel
    And I wait "5s"
    # Pick "Host Shell ↓" — it splits in below the Git panel in the right column
    And I add the "Host Shell" panel below in the menu
    And I wait "5s"
    And I capture screenshot "multi-panel-workspace"
    # Worktrees — spin off an isolated git worktree from the header branch picker, then chat in it
    And I wait "2s"
    And I show caption "Worktrees — branch off into an isolated workspace to work in parallel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on the button with title "Branch"
    And I wait for text "BRANCHES" to appear
    And I wait "3s"
    And I click on "+wt" in the branch picker
    And I wait "6s"
    # Loop creates the worktree and opens it as its own thread — open it from the sidebar
    And I wait "2s"
    And I show caption "Loop creates a git worktree and opens it as its own thread"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "wt-" in the sidebar
    And I wait for "textarea" to be visible
    And I wait "3s"
    And I capture screenshot "worktree"
    # Chat inside the worktree, exactly like any other channel
    And I wait "2s"
    And I show caption "Then work in the worktree exactly like any other channel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "What files are in this worktree? List them briefly." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    # Scheduled tasks (sidebar overlay)
    And I wait "2s"
    And I show caption "Scheduled tasks — run prompts on a cron or interval"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the global tasks panel
    And I wait "3s"
    And I capture screenshot "tasks-panel"
    # Workflows overlay
    And I wait "2s"
    And I show caption "Workflows panel — start runs and watch them across every channel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the workflows panel
    And I wait up to "10s" for text "+ Run" to appear
    And I wait "3s"
    And I capture screenshot "workflows-panel"
    # Settings — global, then per-project
    And I wait "2s"
    And I show caption "Settings — global configuration, as a form or raw JSON"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the settings panel and select "Workflows"
    And I wait "3s"
    And I capture screenshot "settings-panel"
    # Per-project settings — open from the project's gear in the sidebar
    And I wait "2s"
    And I show caption "Each project also has its own settings — open them from its gear in the sidebar"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I hover over "acme-notes" in the sidebar
    And I wait "1s"
    And I click on the button with title "Project config"
    And I wait "4s"
    # Branded outro — fades in and holds at full opacity so the video ends on the card
    And I wait "2s"
    And I show the Loop title card and hold
    Then I stop recording "journey"
