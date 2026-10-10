---
title: UI Bridge
---
The UI bridge lets a script or an agent drive the desktop app: open a channel, switch layout tabs, arrange panes, open a file, and type into, read and wait on agent terminals. Whatever it does happens in a real window, in front of the user.

**Packages:** `internal/uibridge` (hub), `internal/api` (handlers), `cmd/loop` (CLI), `internal/mcpserver` (MCP tools), `app/src/uiBridge` (the window's side)

Related docs: [API: UI Bridge](api.md#ui-bridge) | [Layouts](layouts.md) | [Terminal](terminal.md) | [MCP Server](mcpserver.md)

---

## How It Works

```
loop ui:run ─┐
HTTP client ─┼─► daemon ── /api/ws/ui ──► app window ──► steps run in the UI
ui_run (MCP)─┘     ▲                          │
                   └──── state, results ──────┘
```

- Each app window keeps a WebSocket open to the daemon. It says hello with a client id, then sends its state every time what it shows changes: the open channel, the layout tabs and panes, and each agent terminal's status and whether it's busy.
- A command is a list of steps. The daemon checks it, sends it to one window, and waits for that window's results. The window runs the steps itself, in order, until one fails. The daemon doesn't change the layout itself.
- A command goes to the window the user last focused, unless it names another by its `client_id`. `ui:state` lists the windows.

## Three Ways In

**CLI.** The steps are a JSON array, given as an argument or on stdin. The results print as JSON, and the command exits non-zero when a step fails.

```bash
loop ui:state                 # the windows and what each shows
loop ui:state --watch         # one JSON line per change

loop ui:run '[
  {"op": "select_channel", "channel_id": "ch-1"},
  {"op": "add_pane", "panel": "docker-shell", "next_to": "chat"},
  {"op": "send_input", "pane": "docker-shell", "text": "make test", "submit": true},
  {"op": "wait_for", "pane": "docker-shell", "match": "^(ok|FAIL)", "lines": 200}
]'
```

`--client` picks a window and `--timeout` sets how long to wait for it (default `1m`, at most `10m`).

**HTTP.** `POST /api/ui/commands` takes `{client_id?, timeout?, steps}`. `GET /api/ui/state?after=N` waits for a state newer than version `N`, which is how `--watch` follows changes.

**MCP.** Agents get the `ui_state` and `ui_run` tools. `ui_run`'s steps are typed, so a step with a bad op or field is refused before it reaches the window. Without a leading `select_channel`, the command runs in the agent's own channel.

## Steps

There are steps for channels (`select_channel`), tabs (`set_tab`, `create_tab`, `rename_tab`, `remove_tab`), panes (`add_pane`, `replace_pane`, `remove_pane`, `maximize_pane`, `restore_pane`), the editor (`open_file`) and terminals (`send_input`, `read_output`, `wait_for`). The [API reference](api.md#post-apiuicommands) lists every field.

A `pane` is a pane id from the state, such as `docker-agent-1`, or a panel type, which means the first pane of that type.

## Rules Worth Knowing

- **Steps stop at the first failure.** A failed step has `ok: false` and an `error`, and the steps after it don't run.
- **A command stays in one channel.** That's the channel its `select_channel` opened, or else the one the window showed when the command started. If the user opens another channel while the command runs, the next step fails rather than read from or type into that channel's terminals.
- **`wait_for` waits for new output.** It waits until the terminal's last `lines` match `match`, or, without `match`, until the terminal has been quiet for `quiet_ms`. After a `send_input` to the same pane in the same command, only output printed since then counts. A session that ended counts as done, and a pane that closes fails the step instead of waiting out the timeout.
- **Input to a `docker-agent` pane goes in as one paste**, so a multi-line prompt stays one message. Text containing a bracketed-paste marker (`ESC [200~` or `ESC [201~`) is refused, because it would end the paste early.

## Security

- **No host shells.** The terminal steps work only on `docker-agent` and `docker-shell` panes, which run in containers. Typing into a host shell would run commands on the host, and reading one could show what the host has. This applies to everyone, including the owner: the daemon refuses such a step, and the window refuses it again.
- **Agents stay in their project.**
  - An agent's command must start with a `select_channel`, and every `select_channel` must name a channel, thread or worktree thread of the agent's project. Otherwise the daemon answers `403`.
  - `ui_state` shows an agent nothing for windows on other projects.
- **The window socket is owner-only.** Only the app itself connects to `/api/ws/ui`.
- **The window is shared with the user.** A command moves what the user sees, so agents are told to use it when the user asked to be shown something.
