@frontend @docs
Feature: Documentation walkthrough
  Captures the documentation assets as a set of PER-FEATURE scenarios. Each
  scenario is independently runnable (tagged @docs-<name>) so a single section
  can be re-recorded fast in isolation for iteration —
  `make docs-capture-section SECTION=git` — and each brackets its steps with
  `start recording` / `stop recording "<name>"`, emitting docs/videos/<name>.mp4.
  A full `make docs-capture` run (GODOG_TAGS=@docs) runs every section plus the
  browser clip (tag docs-browser), then stitches them — in the fixed order
  encoded in scripts/test-component.sh — into docs/videos/journey.mp4.
  Still-screenshots are captured at key moments throughout (independent of
  recording).
  Tagged @docs so normal BDD runs skip it (GODOG_TAGS defaults to ~@docs);
  run via `make docs-capture`, which sets LOOP_DOCS_CAPTURE so the capture steps
  write assets (screenshots into docs/static/images/features, the MP4s into the
  gitignored docs/videos).

  # All scenarios share ONE sample "acme-notes" project + channel (built once via
  # sync.Once). In a full run the BACKEND state (git history, indexed memory,
  # sessions) accumulates across scenarios in file order — so e.g. the git
  # section commits a branch the git-panel section later shows — while each
  # scenario gets a FRESH browser and re-establishes its own UI from the default
  # chat+git layout. Run alone, a view-only section (sessions, git-panel) shows
  # less, but still validates its own capture/timing.
  # Rhythm per stop: record -> caption (explains the next action) -> pause ->
  # action -> settle. Captions are hidden before the action so they never obscure
  # the panel or a screenshot.
  Background:
    Given I set up a sample project channel
    And I open the app in a browser
    And I wait for text "acme-notes" to appear
    And I click on "acme-notes" in the sidebar
    And I wait for "textarea" to be visible
    And I show the mouse cursor

  @docs-intro
  Scenario: Intro
    # Branded title card, then fade into the app
    Given I show the Loop intro card
    When I start recording
    And I wait "3s"
    And I fade out the Loop title card
    And I hide caption
    Then I stop recording "01_intro"

  @docs-chat
  Scenario: Chat — live agent reply
    When I start recording
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
    And I wait "2s"
    # Paste an image into the input — Loop saves it to the workspace and inserts
    # its file path. Send it as its OWN message (a short lead-in + the image) so the
    # agent reads the screenshot separately, and the input is clear for whatever
    # comes next (otherwise, in the single-take journey, the next prompt would glue
    # onto the leftover path).
    And I show caption "Paste an image — Loop saves it and inserts its file path"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Here's a screenshot of the UI — tell me what you see. " into "textarea"
    And I paste an image into "textarea"
    And I wait "3s"
    And I capture screenshot "chat-image-paste"
    And I wait "2s"
    And I press Enter
    And I wait up to "90s" for "button[title='Stop']" to disappear
    And I wait "4s"
    Then I stop recording "02_chat"

  @docs-gate
  Scenario: Security gate
    # A REAL gate: agentgate command_rules hold the agent's `git commit` for
    # approval (see scripts/test-component.sh). Approve it, then see the
    # committed change land in the Git panel's Branches Diff.
    When I start recording
    And I wait "2s"
    And I show caption "Security gate — risky commands pause for your approval"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Create a branch called gate-demo, append a line '<!-- reviewed -->' to README.md, and commit it with a concise message." into "textarea"
    And I press Enter
    # The commit is held — the approval card appears in chat.
    And I wait up to "120s" for text "Allow once" to appear
    And I wait "3s"
    And I capture screenshot "gate-approval"
    # Approve it — the held commit proceeds.
    And I wait "2s"
    And I show caption "Press Allow — the commit is approved and runs"
    And I wait "4s"
    And I hide caption
    And I click on the button with text "Allow once"
    # Give the approved commit a moment to land (don't block on the agent fully
    # finishing — it may keep working after the commit).
    And I wait "10s"
    # The approved commit lands — review it in Branches Diff.
    And I show caption "Approved — the commit lands; review it in Branches Diff"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click "Branches Diff" in the git panel
    And I wait "4s"
    Then I stop recording "03_gate"

  @docs-shortcuts
  Scenario: Prompt shortcuts
    When I start recording
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
    And I wait "2s"
    And I wait up to "90s" for "button[title='Stop']" to disappear
    And I wait "4s"
    Then I stop recording "04_shortcuts"

  @docs-git
  Scenario: Git workflow
    # Ask the agent to change a file, inspect the uncommitted diff, commit it on a
    # branch, then review history (Commits, Branches Diff).
    When I start recording
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
    # Commit it on a new branch — so Branches Diff below has something to show.
    And I wait "2s"
    And I show caption "Ask the agent to put it on a new branch and commit"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Create a new branch called add-healthz, then commit that change to it with a concise message." into "textarea"
    And I press Enter
    # The commit is gated — approve it so it proceeds.
    And I wait up to "90s" for text "Allow once" to appear
    And I click on the button with text "Allow once"
    # Give the approved commit a moment to land (don't block on the agent
    # fully finishing — it may keep working after the commit).
    And I wait "10s"
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
    Then I stop recording "05_git"

  @docs-editor
  Scenario: Editor
    # Open a file, edit it inline, then ask the agent to commit it.
    When I start recording
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
    # Ask the agent in chat to commit the change — on its own branch, so the Git
    # panel's Branches Diff has something to compare.
    And I wait "2s"
    And I show caption "Then just ask the agent to put it on a branch and commit — same repo, same workspace"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Create a new branch called update-readme, then commit my change to README.md to it with a concise message." into "textarea[placeholder*='Ask Loop anything']"
    And I press Enter
    # The commit is gated — approve it so it proceeds.
    And I wait up to "90s" for text "Allow once" to appear
    And I click on the button with text "Allow once"
    # Give the approved commit a moment to land (don't block on the agent
    # fully finishing — it may keep working after the commit).
    And I wait "10s"
    Then I stop recording "06_editor"

  @docs-memory
  Scenario: Memory
    When I start recording
    And I wait "2s"
    And I show caption "Memory — searchable long-term memory the agent can recall"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Memory']"
    # The background re-indexer (Ollama embeddings, 30s ticker) indexes the sample
    # project's CLAUDE.md / README.md — wait for the tree to populate before
    # capturing. Generous timeout: the first run cold-starts the Ollama sidecar
    # and pulls the embedding model (~270MB), which can take a couple of minutes
    # (the model persists in a named volume, so later runs are fast).
    And I wait up to "300s" for text "CLAUDE.md" to appear
    # The panel auto-opens the first indexed file; wait for its content to finish
    # loading so the viewer shows real content, not the "Loading..." placeholder.
    And I wait up to "30s" for text "Loading..." to disappear
    And I wait "2s"
    And I capture screenshot "memory-panel"
    And I wait "2s"
    # Now recall it from chat — the agent answers using the search_memory MCP tool.
    And I show caption "Recall it from chat — the agent answers via the search_memory tool"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Chat']"
    And I wait for "textarea" to be visible
    And I wait "2s"
    And I type "Use Loop's search_memory tool to look up and tell me the coding conventions for this notes service." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    And I capture screenshot "memory-recall"
    And I wait "2s"
    Then I stop recording "07_memory"

  @docs-terminal
  Scenario: Terminal
    # Open a Docker shell, run a saved command, then split in a Docker Agent
    # terminal that resumes this channel's session inside the container.
    When I start recording
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
    # resumes THIS channel's Claude session right inside the container.
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
    # Paste an image straight into the agent terminal — Loop uploads it and drops
    # the saved file path into the prompt, the same UX as the chat input. Send the
    # same prompt as the chat section and wait for the agent to answer.
    And I wait "2s"
    And I show caption "Paste an image into the terminal — ask about it and send, just like chat"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I type "Here's a screenshot of the UI — tell me what you see. " into the agent terminal
    And I paste an image into the agent terminal
    And I wait "3s"
    And I capture screenshot "terminal-image-paste"
    And I wait "2s"
    And I submit the agent terminal
    And I wait "30s"
    Then I stop recording "08_terminal"

  @docs-git-panel
  Scenario: Git panel
    # The full Git tab (diff, branches, commits, worktrees).
    When I start recording
    And I wait "2s"
    And I show caption "Git — review the diff, branches, commits, and worktrees"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Git']"
    And I wait "3s"
    And I capture screenshot "git-panel"
    And I wait "2s"
    Then I stop recording "09_git-panel"

  @docs-browser
  Scenario: Browser
    # The agent drives a real headless Chrome through its browser MCP tools —
    # navigate to a page, then read it back via the console tools.
    When I start recording
    And I wait "1s"
    And I show caption "Browser — the agent drives a real Chrome through its tools"
    And I wait "4s"
    And I hide caption
    And I wait "1s"
    And I click on "[data-testid='layout-tab-Browser Chat']"
    And I wait "2s"
    And I show caption "Ask it to open a page and read it back via the console"
    And I wait "4s"
    And I hide caption
    And I wait "1s"
    And I type "Use your browser tools to navigate to https://example.com, then use the console to extract and report the page's main heading text." into "textarea"
    And I press Enter
    And I wait "2s"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "4s"
    And I capture screenshot "browser-agent"
    And I wait "2s"
    Then I stop recording "10_browser"

  @docs-sessions
  Scenario: Sessions
    When I start recording
    And I wait "2s"
    And I show caption "Sessions — browse and resume past Claude sessions"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Sessions']"
    And I wait "3s"
    And I capture screenshot "sessions"
    And I wait "2s"
    Then I stop recording "11_sessions"

  @docs-swarm
  Scenario: Swarm
    When I start recording
    And I wait "2s"
    And I show caption "Swarm — run several agents in parallel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Swarm']"
    And I wait "3s"
    Then I stop recording "12_swarm"

  @docs-canvas
  Scenario: Canvas
    When I start recording
    And I wait "2s"
    And I show caption "Canvas — arrange panels freely on an infinite canvas"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Canvas']"
    And I wait "3s"
    Then I stop recording "13_canvas"

  @docs-playground
  Scenario: Playground
    # Ask the agent to build a live HTML/CSS/JS sandbox via MCP.
    When I start recording
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
    And I wait "2s"
    Then I stop recording "14_playground"

  @docs-kanban
  Scenario: Kanban
    When I start recording
    And I wait "2s"
    And I show caption "Kanban — track tickets across Open, In Progress, and Closed"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Kanban']"
    And I wait "3s"
    And I capture screenshot "kanban-board"
    And I wait "2s"
    Then I stop recording "15_kanban"

  @docs-workflows-tab
  Scenario: Workflows tab
    # Seed a real run (bdd-test-workflow is in the harness config) so the tab
    # shows a live DAG with a completed run instead of an empty state.
    Given I start a workflow run for "bdd-test-workflow" via API
    When I start recording
    And I wait "2s"
    And I show caption "Workflows — declarative multi-step pipelines with a live DAG"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I click on "[data-testid='layout-tab-Workflows']"
    And I wait "5s"
    And I capture screenshot "workflows-tab"
    And I wait "2s"
    Then I stop recording "16_workflows-tab"

  @docs-quality
  Scenario: Quality
    # Split the panel into the Chat layout, run a scan.
    When I start recording
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
    # Post-scan marker: the header renders `geo-mean N.NNN`. (Don't wait on the
    # metric-card labels — they're CSS-uppercased and innerText reflects that.)
    And I wait up to "60s" for text "geo-mean" to appear
    And I wait "3s"
    And I capture screenshot "quality-panel"
    And I wait "2s"
    Then I stop recording "17_quality"

  @docs-multi-panel
  Scenario: Multi-panel workspace
    # Compose a custom workspace: split a Host Shell under the Git panel.
    When I start recording
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
    And I wait "2s"
    Then I stop recording "18_multi-panel"

  @docs-worktrees
  Scenario: Worktrees
    # Open the acme-notes +wt picker, pick the main branch to create the
    # worktree off it, then chat inside the new worktree thread.
    When I start recording
    And I wait "2s"
    And I show caption "Worktrees — branch off into an isolated workspace to work in parallel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    # Quickest path: hover a channel row in the sidebar and click +wt to pick a base branch
    And I show caption "Hover a channel and click +wt to branch off from any branch"
    And I wait "3s"
    And I hide caption
    And I hover over "acme-notes" in the sidebar
    And I wait "1s"
    # Scope the +wt click to the acme-notes row (every channel has its own +wt)
    And I click the worktree button for "acme-notes" in the sidebar
    And I wait for "[data-testid='sidebar-worktree-picker']" to be visible
    And I wait "2s"
    And I capture screenshot "sidebar-worktree"
    And I wait "2s"
    # Pick the base branch (main) right in the acme-notes picker to branch off it
    And I show caption "Pick the base branch — here, main — to branch off from"
    And I wait "3s"
    And I hide caption
    And I wait "1s"
    And I click branch "main" in the sidebar worktree picker
    And I wait "6s"
    # Loop creates the worktree off acme-notes/main and opens it as its own thread — open it from the sidebar
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
    Then I stop recording "19_worktrees"

  @docs-tasks
  Scenario: Scheduled tasks
    When I start recording
    And I wait "2s"
    And I show caption "Scheduled tasks — run prompts on a cron or interval"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the global tasks panel
    And I wait "3s"
    And I capture screenshot "tasks-panel"
    And I wait "2s"
    # Select the seeded task and run it once, on demand
    And I show caption "Select a task and Run Now to trigger it immediately"
    And I wait "3s"
    And I hide caption
    And I wait "1s"
    And I click on "Summarise" in the global tasks panel
    And I wait "2s"
    And I click button "Run Now" in the global tasks panel
    And I wait "6s"
    # The run opens as its own thread under the project — open it from the sidebar
    And I show caption "The run opens as its own thread — open it from the sidebar"
    And I wait "4s"
    And I hide caption
    And I wait "1s"
    And I press Escape
    And I wait "1s"
    And I wait up to "30s" for text "task #" to appear
    And I click on "task #" in the sidebar
    And I wait for "textarea" to be visible
    And I wait "3s"
    And I capture screenshot "task-thread"
    And I wait up to "120s" for "button[title='Stop']" to disappear
    And I wait "2s"
    Then I stop recording "20_tasks"

  @docs-workflows-panel
  Scenario: Workflows overlay
    When I start recording
    And I wait "2s"
    And I show caption "Workflows panel — start runs and watch them across every channel"
    And I wait "4s"
    And I hide caption
    And I wait "2s"
    And I open the workflows panel
    And I wait up to "10s" for text "+ Run" to appear
    And I wait "2s"
    # Trigger the seeded bdd-test-workflow right from the panel
    And I show caption "Start a run right from the panel — pick a workflow and go"
    And I wait "3s"
    And I hide caption
    And I wait "1s"
    And I click button "+ Run" in the workflows panel
    And I wait for text "Start Workflow" to appear
    And I wait "2s"
    And I click on the button with text "Start"
    And I wait "6s"
    And I capture screenshot "workflows-panel"
    And I wait "2s"
    Then I stop recording "21_workflows-panel"

  @docs-settings
  Scenario: Settings
    # Global, then per-project.
    When I start recording
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
    Then I stop recording "22_settings"

  @docs-outro
  Scenario: Outro
    # Branded card fades in and holds so the montage ends on it.
    When I start recording
    And I wait "1s"
    And I show the Loop title card and hold
    And I wait "2s"
    Then I stop recording "23_outro"
