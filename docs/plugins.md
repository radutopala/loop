# Plugins

A Loop plugin is an OCI image that Loop installs by digest and runs in its own container, one per
channel. The plugin serves HTTP for Loop, calls Loop's Plugin API with a channel-scoped token,
asks Loop for agent runs that call its tools, and can draw panels in the workspace from a static
web bundle.

Plugins are off unless `plugins.enabled` is set in the global config. While
`gates.agentgate.enabled` is on, plugin containers run under agentgate (see [Agentgate](#agentgate)).

## Enabling a plugin

An installed plugin runs only in projects that enable it:

```jsonc
// <project>/.loop/config.json
{ "plugins": { "cogtest": { "enabled": true } } }
```

`plugins.<id>.enabled` in the global config sets the default for every project; a project's own
value overrides it. Like the rest of a project config, a project's value applies once the owner
trusts that config; until then the last approved version's values do. Settings → Plugins sets
both, keeping the file's comments.

A project is a channel: its threads and worktree threads use the channel's container. In a
project that doesn't enable the plugin, its panels are left out of the add-pane menus, a pane
already open says so, and every call to the plugin answers `409`. A container already running
there stays up until it's idle or stopped.

## Managing plugins

Settings → Plugins installs (after showing the capabilities the image asks for), upgrades and
removes plugins, and lists each one's containers with a Stop button. `loop plugin` does the same
from the shell. The API behind both:

| Route | |
|---|---|
| `GET /api/plugins?channel_id=` | the installed plugins with their `actions`, `enabled_by_default` and `containers` (`channel_id`, `channel_name`, `status`, `last_error`, `updated_at`); with `channel_id`, also `enabled` and `project_enabled` (the project file's own value) for that channel's project, and a top-level `project_trusted` |
| `POST /api/plugins/inspect` | body `{"image", "dev"}`: pulls and checks an image, installing nothing |
| `POST /api/plugins` | body `{"image", "dev", "digest", "approve": […]}`: installs or upgrades |
| `DELETE /api/plugins/{id}?purge=` | removes a plugin; `purge` also deletes its data in every channel |
| `POST /api/plugins/{id}/channels/{channel}/stop` | stops the plugin's container in a channel, keeping its data |

Installing, removing and stopping send a `plugins.changed` event to every client.

## Image layout

```
/loop-plugin/plugin.json      manifest (required)
/loop-plugin/ui/<panel>/...   static UI bundle per panel (index.html plus assets)
/data/                        must exist, owned by the image's USER
```

The image carries the label `dev.loop.plugin=1`, runs as a non-root `USER` (a numeric
`uid:gid` works) and serves HTTP on `$LOOP_PLUGIN_PORT`. See
`test/component/testdata/plugins/echo` for the smallest working plugin.

## Manifest

`plugin.json` is JSON with comments and trailing commas allowed.

```jsonc
{
  "id": "echo",                    // [a-z][a-z0-9-]{1,31}
  "name": "Echo",
  "version": "0.1.0",
  "api": "1",
  "capabilities": ["agent.readonly", "files.read", "ui.panel"],
  "agents": [{ "id": "say", "read_only": true, "tools": ["echo"] }],
  "tools": [{ "name": "echo", "description": "Returns its input", "input_schema": { "type": "object" } }],
  "actions": [{ "id": "ping" }],
  "ui": { "panels": [{ "id": "main", "label": "Echo", "entry": "ui/main/index.html", "singleton": true }] },
  "resources": { "memory": "128m", "cpus": 0.5 },
}
```

Capabilities are approved by the owner at install and checked on every call:

| Capability | Grants |
|---|---|
| `files.read` | `GET /channel/files`, `GET /channel/tree`, and the channel's dir mounted read-only |
| `git.read` | `GET /channel/git/recent` |
| `quality.read` | `GET /channel/quality/hotspots` |
| `agent.readonly` | runs of read-only agents |
| `agent.write` | runs of any agent |
| `ui.panel` | panels |
| `ui.notify` | `POST /notify`, the bridge's `ui.notify` |
| `ui.open_file` | the bridge's `ui.open_file` |

## Container environment

| Variable | Value |
|---|---|
| `LOOP_PLUGIN_ID` | the plugin's id |
| `LOOP_CHANNEL_ID`, `LOOP_CHANNEL_DIR` | the channel the container serves and its dir on the host, mounted read-only at the same path once `files.read` is approved |
| `LOOP_PLUGIN_PORT` | the port to serve on |
| `LOOP_API_URL` | Loop's API, for the Plugin API |
| `LOOP_PLUGIN_TOKEN_FILE` | the plugin token for the Plugin API |
| `LOOP_PLUGIN_SECRET_FILE` | the secret Loop sends to the plugin |
| `LOOP_DATA_DIR` | `/data`, the host dir `~/.loop/plugins/<id>/data/<channel>/`, which outlives the container |

The container is started when first needed and stopped when idle; keep state in `/data`. It runs
as the daemon's user, so the files it writes there belong to you on the host. A daemon running as
root, in a container, names the user with `LOOP_HOST_UID` and `LOOP_HOST_GID`; plugin containers
never run as root. Removing the plugin keeps the files; `--purge` deletes them.

The channel's dir is the working tree of the channel's checkout, on whatever branch it has, with
uncommitted changes. Threads use their channel's container, so worktree threads' branches aren't
seen.

## Agentgate

While `gates.agentgate.enabled` is on, a plugin container runs its image's entrypoint and command
under `loop syscallwrap`, which starts as root, drops to the daemon's user and traps the
plugin's file, exec and unix-socket syscalls. Nobody is there to answer a prompt, so whatever the
policy doesn't allow is denied. The policy is the manifest's `gate` rules first, then Loop's
baseline:

- every file op under `/data` and `/tmp`, and on the usual `/dev` files
- reads, stats and listings anywhere else
- any exec
- no unix-socket connects

```jsonc
"gate": {
  "files": [{ "paths": ["/data/cache/**"], "operations": ["delete"], "decision": "deny" }],
  "commands": [{ "commands": ["curl", "wget"], "decision": "deny" }],
}
```

The manifest may only allow or deny; `approve` is refused at install. Decisions go to an audit
log under `~/.loop/plugins/<id>/audit/<channel>/`, kept per `gates.audit`.

The gate needs a linux `loop` binary in the container, which plugin images don't have. Loop
mounts the release matching the daemon's version, downloaded on first use, checked against the
release's checksums and cached under `~/.loop/cache/gate-bin/`. A dev build has no release, so
build one with `make build-linux` and point `plugins.gate_binary` at it.

## Loop → plugin

Every request carries `Authorization: Bearer <contents of LOOP_PLUGIN_SECRET_FILE>`; refuse
any other.

| Method and path | Body | Answer |
|---|---|---|
| `GET /healthz` | | `200 {"api": "1", "version": "…"}` |
| `POST /actions/{action}` | `{"trigger": "manual"\|"schedule", "task_id": 0}` | `202` quickly; do the work afterwards |
| `POST /tools/{tool}` | `{"run_id", "agent", "thread_id", "input": {…}}` | `200 {"content": "…", "is_error": false}`; the agent sees `content`, and `is_error` lets it retry |
| `POST /runs/{run_id}/done` | `{"status": "done"\|"failed", "error": "…"}` | `204`; sent when an agent run the plugin started ends |
| `ANY /x/{path}` | whatever the plugin's UI sends | the plugin's own API for its UI, proxied by Loop with method, query, body and `Content-Type` |

Tool input is validated against the tool's `input_schema` before it reaches the plugin.

## Plugin → Loop: Plugin API v1

Calls go to `$LOOP_API_URL/api/plugin/v1/...` with `Authorization: Bearer <contents of
LOOP_PLUGIN_TOKEN_FILE>`. The token is held to the plugin's channel, so no route takes a
channel id. A route needing a capability the owner didn't approve answers `403`.

| Route | Capability | Answer |
|---|---|---|
| `GET /channel` | | `{"id", "name", "dir_path", "platform", "model", "effort"}` |
| `GET /channel/files?path=&start=&end=` | `files.read` | `{"path", "content", "start", "end", "total_lines"}`; lines are 1-based and inclusive, both optional (without them `content` is the whole file); `.git/` is refused; `404` if missing, `400` for a directory or a range past the end, `413` over the size limit |
| `GET /channel/tree?glob=` | `files.read` | `{"files": ["a/b.go", …], "truncated": true}`: the files git tracks (or every file outside `.git/` without git), optionally filtered by a glob matched against the path or its base name; at most 50,000, with `truncated` set when there were more |
| `GET /channel/git/recent?days=` | `git.read` | `{"commits": [{"sha", "subject", "author", "time"}], "files": [{"path", "commits"}]}`; files most-changed first; `days` defaults to 14, at most 365; the last 500 commits; `409` when the dir isn't a git repository |
| `GET /channel/quality/hotspots?limit=` | `quality.read` | the latest quality scan's functions, worst first: `{"functions": [{"path", "name", "start_line", "cyclomatic", "cognitive", "max_nesting", "param_count", "loc", "score"}]}`; `503` before the first scan |
| `POST /runs` | `agent.readonly` / `agent.write` | body `{"agent", "prompt", "model"?, "effort"?}` → `202 {"run_id", "thread_id"}`; see below |
| `POST /events` | | body `{"type", "data"}` → `204`; sent to the plugin's panels in the channel as `type` |
| `POST /notify` | `ui.notify` | body `{"title", "body"}` → `204`; a desktop notification |

### Agent runs

`POST /runs` starts a run of one of the plugin's agents in the channel's hidden `plugin:<id>`
thread, with a fresh session. The agent gets the tools the manifest gives it as MCP tools named
`plugin_<tool>`; a read-only agent runs read-only. The plugin sends the whole prompt. When the run
ends Loop calls `POST /runs/{run_id}/done` on the plugin.

## Panels

A panel is a static bundle under `/loop-plugin/ui/`, shown in a sandboxed iframe. Its files are
served from a signed link with a content security policy that:

- gives the page an opaque origin (no cookies, storage or access to the app);
- loads scripts, styles, images, fonts and `fetch`es only from the plugin's own bundle, plus
  `data:`/`blob:` for images, fonts and media;
- blocks inline `<script>`, `eval`, frames, form submission and navigation away.

So a bundle ships everything it needs, uses script files, and gets its data through the bridge.

A plugin approved for `ui.panel` adds its panels to the workspace's add-pane menus, in split and
canvas layouts, after the built-in panels. A `singleton` panel can be shown once per layout. A
pane in a thread, however deeply nested, talks to the plugin's container of the thread's channel.

Once the plugin has started an agent run in the project, its panes show an **agent** label at the
right of their header. It opens the agent view over the layout, as the Learn view opens: the panel
on the left half, the run's chat on the right, and the logo on their seam, which moves while a run
runs. The label closes it again. The panel isn't reloaded on the way. The chat is read-only: only
the plugin starts runs in its thread, each in a new session of its own.

### Bridge

On load the host posts `{"type": "loop-plugin:init"}` to the frame with a `MessagePort` in
`event.ports[0]`. The port carries JSON-RPC 2.0: the frame sends
`{"jsonrpc": "2.0", "id": 1, "method": "…", "params": {…}}` and gets back `result` or
`error: {"code", "message"}`; notifications from the host have no `id`.

| Method | Params | Result |
|---|---|---|
| `api.request` | `{"method", "path", "body"?}` | `{"status", "body"}`: the plugin's `/x/{path}` in the panel's channel, JSON bodies decoded |
| `context.get` | | `{"pluginId", "panel", "channelId", "channelName", "dirPath"}` |
| `state.get` | | the panel's saved UI state, or `null` |
| `state.set` | `{"value"}` | `null`; up to 64 KB of JSON, kept per plugin, channel and panel |
| `theme.get` | | `{"mode": "dark"\|"light", "vars": {"--bg": "…", …}}` |
| `ui.notify` | `{"title", "body"}` | `null`; needs `ui.notify` |
| `ui.open_file` | `{"path", "line"?}` | `null`; opens a file of the channel in the editor; needs `ui.open_file` |

The theme's `vars` are `--bg`, `--surface`, `--fg`, `--muted`, `--border`, `--accent`,
`--success`, `--danger`, `--font` and `--mono`; a panel sets them on its root element.

Host notifications:

| Method | Params |
|---|---|
| `event` | `{"type", "data"}`: an event the plugin's container sent with `POST /events` |
| `theme` | as `theme.get`, when the theme changes |
