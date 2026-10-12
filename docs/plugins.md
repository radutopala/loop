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

A project is a channel, and its threads and worktree threads are part of it: they follow the
channel's project config. Each channel, thread and worktree thread runs a container of its own,
with its own data, and the plugin's agent runs go in a hidden thread of each. In a
project that doesn't enable the plugin, its panels are left out of the add-pane menus, a pane
already open says so, and every call to the plugin answers `409`. A container already running
there is stopped as soon as Loop writes the config or the owner trusts it, and within a minute of
an edit made outside Loop.

### Settings

A plugin declares its settings in its manifest, and they're set under `plugins.<id>.settings`:

```jsonc
// <project>/.loop/config.json
{ "plugins": { "cogtest": { "enabled": true, "settings": { "question_count": 8 } } } }
```

A setting takes its manifest default, then the global config's value, then the project's,
setting by setting. Unlike `enabled`, a project's settings apply without the owner trusting its
config: they only tune the plugin. A value for a setting the manifest doesn't declare, or one its
schema refuses, is left out, so the value below it applies, and Settings → Plugins says so. Settings → Plugins edits both: the
global values on Global → Plugins, the project's on Project → Plugins; an emptied field unsets
its value. The plugin reads its settings with `GET /channel`, and a panel with the bridge's
`settings.get`.

## Managing plugins

Settings → Plugins installs (after showing the capabilities the image asks for), upgrades and
removes plugins, and lists each one's containers with a Stop button. `loop plugin` does the same
from the shell. The API behind both:

| Route | |
|---|---|
| `GET /api/plugins?channel_id=` | the installed plugins with their `actions`, `enabled_by_default`, `settings_schema`, `global_settings` and `containers` (`channel_id`, `channel_name`, `status`, `last_error`, `updated_at`); with `channel_id`, also `enabled` and `project_enabled` (the project file's own value), `project_settings`, `settings` and `settings_problems` for that channel's project, and a top-level `project_trusted` |
| `POST /api/plugins/inspect` | body `{"image", "dev"}`: pulls and checks an image, installing nothing |
| `POST /api/plugins` | body `{"image", "dev", "digest", "approve": […]}`: installs or upgrades |
| `DELETE /api/plugins/{id}?purge=` | removes a plugin; `purge` also deletes its data in every channel |
| `POST /api/plugins/{id}/channels/{channel}/stop` | stops the plugin's container in a channel, keeping its data |

Installing, removing and stopping send a `plugins.changed` event to every client, and so do every
config write Loop makes, trusting a project config, and the idle or disabled containers the
daemon stops on its own; open panes and Settings → Plugins list the plugins again on it.

## Image layout

```
/loop-plugin/plugin.json      manifest (required)
/loop-plugin/ui/<panel>/...   static UI bundle per panel (index.html plus assets)
/data/                        must exist, owned by the image's USER
```

The image carries the label `dev.loop.plugin=1`, runs as a non-root `USER` (a numeric
`uid:gid` works) and serves HTTP on `$LOOP_PLUGIN_PORT`. `/loop-plugin` may hold at most 50 MB;
install refuses a bigger one. See
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
  "actions": [
    { "id": "ping" },
    // needs "schedule" in capabilities
    { "id": "daily", "default_schedule": { "type": "cron", "schedule": "0 9 * * *", "enabled": true } },
  ],
  "settings": { "count": { "type": "integer", "title": "Count", "minimum": 1, "maximum": 10, "default": 3 } },
  "ui": {
    "panels": [{ "id": "main", "label": "Echo", "entry": "ui/main/index.html", "singleton": true }],
    // needs "ui.notification.card" in capabilities
    "cards": [{ "id": "score", "entry": "ui/score.html" }],
  },
  "resources": { "memory": "128m", "cpus": 0.5 },
}
```

Each setting is a schema: `type` (`string`, `integer`, `number`, `boolean`, `array` or
`object`), with `title`, `description`, `default`, `enum`, `minimum`, `maximum`, `pattern`,
`items`, `properties` and `additionalProperties`. Defaults are checked at install.

### Events

`"events": ["message.created", "agent.status", "quality.scanned"]` subscribes the plugin to those
Loop events, and needs `events.subscribe`. Loop sends each one to `POST /events` on the
container of the channel it happened in, starting the container if it isn't running, when the
channel's project enables the plugin. An event in one of the channel's threads or worktree
threads carries the thread's id and goes to the thread's own container; `"thread_events":
"channel"` sends it to the channel's container instead, for a plugin that keeps one view of the
whole project, and `"both"` to both. Events in hidden threads, such as the plugin's own agent
runs, aren't sent. Delivery is best effort: each event is sent once, in no set order, and
dropped when the plugin doesn't take it.

### Scheduled actions

An action with a `default_schedule` (type `cron` or `interval`) gets a scheduled task in each
channel whose project enables the plugin, created once, enabled as `enabled` says. When it
runs, Loop calls `POST /actions/{action}` on the channel's container with `"trigger":
"schedule"` and the task's id, starting the container if it isn't running. The tasks show in
the Tasks panel like any other, where the owner can change their schedule, turn them off or
delete them; a deleted one isn't created again. A project that stops enabling the plugin
pauses its tasks, and enabling it again resumes them, except those the owner turned off.
Only desktop app channels get them: threads and worktree threads run their channel's, and
plugins don't run in Slack or Discord. Removing the plugin deletes
them.

Capabilities are approved by the owner at install and checked on every call:

| Capability | Grants |
|---|---|
| `files.read` | `GET /channel/files`, `GET /channel/tree` |
| `git.read` | `GET /channel/git/recent` |
| `quality.read` | `GET /channel/quality/hotspots` |
| `agent.readonly` | runs of read-only agents |
| `agent.write` | runs of any agent |
| `schedule` | actions' `default_schedule` tasks |
| `ui.panel` | panels |
| `ui.notification.notify` | `POST /notify`, the bridge's `ui.notification.notify` |
| `ui.open_file` | the bridge's `ui.open_file` |
| `ui.copy` | the bridge's `ui.copy` |
| `memory.write` | `PUT /memory/{name}`, `DELETE /memory/{name}` |
| `events.subscribe` | the manifest's `events`, sent to `POST /events` |
| `ui.run` | `POST /ui/commands`: drives an app window, in the plugin's channel and its threads |
| `ui.badge` | `PUT /badge`, `DELETE /badge`: a short label on the channel's sidebar row |
| `ui.notification.card` | the manifest's `ui.cards`, `POST /cards`: pages in the channel's notification drawer |

## Container environment

| Variable | Value |
|---|---|
| `LOOP_PLUGIN_ID` | the plugin's id |
| `LOOP_CHANNEL_ID`, `LOOP_CHANNEL_DIR` | the channel the container serves and its dir on the host. The dir isn't mounted: read its files through `GET /channel/files` and `GET /channel/tree` |
| `LOOP_PLUGIN_PORT` | the port to serve on |
| `LOOP_API_URL` | Loop's API, for the Plugin API |
| `LOOP_PLUGIN_TOKEN_FILE` | the plugin token for the Plugin API |
| `LOOP_PLUGIN_SECRET_FILE` | the secret Loop sends to the plugin |
| `LOOP_DATA_DIR` | `/data`, the host dir `~/.loop/plugins/<id>/data/<channel>/`, which outlives the container |

The container is started when first needed and stopped when idle; keep state in `/data`. A
`docker restart` of it is fine: Loop finds the new port it's published on. It runs
as the daemon's user, so the files it writes there belong to you on the host. A daemon running as
root, in a container, names the user with `LOOP_HOST_UID` and `LOOP_HOST_GID`; plugin containers
never run as root. Removing the plugin keeps the files; `--purge` deletes them.

The channel's dir is the working tree of the channel's checkout, on whatever branch it has, with
uncommitted changes. A thread's is its channel's; a worktree thread's is its worktree, on the
worktree's branch.

## Agentgate

While `gates.agentgate.enabled` is on, a plugin container runs its image's entrypoint and command
under `loop syscallwrap`, which starts as root, drops to the daemon's user and traps the
plugin's file, exec and unix-socket syscalls. Nobody is there to answer a prompt, so whatever the
policy doesn't allow is denied. The policy is a deny on running files from `/data`, `/tmp` and
`/dev/shm`, the dirs a plugin can write, so it only runs what its image ships. Then come the
manifest's `gate` rules, then Loop's baseline:

- every file op under `/data` and `/tmp`, and on the usual `/dev` files
- reads, stats and listings anywhere else
- any exec of a file outside those dirs
- no unix-socket connects

```jsonc
"gate": {
  "files": [{ "paths": ["/data/cache/**"], "operations": ["delete"], "decision": "deny" }],
  "commands": [{ "commands": ["curl", "wget"], "decision": "deny" }],
}
```

The manifest may only allow or deny; `approve` is refused at install. Decisions go to an audit
log under `~/.loop/plugins/<id>/audit/<channel>/`, kept per `gates.audit`. Unlike an agent
container, a plugin container keeps Docker's default seccomp profile; the gate's filter stacks
on top of it.

The gate needs a linux `loop` binary in the container, which plugin images don't have. Loop
mounts the release matching the daemon's version, downloaded on first use, checked against the
release's checksums and cached under `~/.loop/cache/gate-bin/`. A dev build has no release, so
build one with `make build-linux` and point `plugins.gate_binary` at it.

## Loop → plugin

Both halves of the contract are published as OpenAPI 3.1 specs in the SDK module:
[`plugin-contract-v1.yaml`](../pluginsdk/openapi/plugin-contract-v1.yaml) for what a plugin
serves, and [`plugin-api-v1.yaml`](../pluginsdk/openapi/plugin-api-v1.yaml) for the Plugin API.
Loop's and the SDK's tests check the routes, capabilities, success statuses and the shape of each
JSON answer against them. The SDK's wire types are generated from them with oapi-codegen into
`openapi/pluginapi` and `openapi/plugincontract`: after changing a spec, run `make sdk-gen`;
`make lint` and CI fail while the generated files are out of date (`make sdk-gen-check`).

Every request carries `Authorization: Bearer <contents of LOOP_PLUGIN_SECRET_FILE>`; refuse
any other.

| Method and path | Body | Answer |
|---|---|---|
| `GET /healthz` | | `200 {"api": "1", "version": "…"}` |
| `POST /actions/{action}` | `{"trigger": "manual"\|"schedule"\|"workflow", "task_id": 0, "run_id", "input": {…}}` | `202` quickly; do the work afterwards |
| `POST /tools/{tool}` | `{"run_id", "agent", "thread_id", "input": {…}}` | `200 {"content": "…", "is_error": false}`; the agent sees `content`, and `is_error` lets it retry; a workflow takes `content` as the node's output, and `is_error` fails the node |
| `POST /runs/{run_id}/done` | `{"status": "done"\|"failed", "error": "…"}` | `204`; sent when an agent run the plugin started ends |
| `POST /events` | `{"type", "channel_id", "thread_id", "data": {…}, "timestamp"}` | `204`; a Loop event the manifest subscribes to, with `data` as the app's events WebSocket carries it |
| `ANY /x/{path}` | whatever the plugin's UI sends | the plugin's own API for its UI, proxied by Loop with method, query, body and `Content-Type` |

Tool input is validated against the tool's `input_schema` before it reaches the plugin.

A workflow's `plugin` node runs an action or calls a tool on the container of the run's channel,
starting it if it isn't running, when the channel's project enables the plugin (see
[Workflows: Plugin Node](workflows.md#plugin-node-type-plugin)). Both come with `run_id`
`workflow:<run id>:<node id>` and the node's rendered `input`. Its action gets `"trigger":
"workflow"`, and the node succeeds once the plugin accepts it, so an action's result never
reaches the workflow: a workflow that needs one calls a tool. Its tool call may name any tool in
the manifest, not only those given to an agent, comes with an empty `agent` and `thread_id`,
and may run past 60 seconds when the node's `timeout` is longer.

### When a plugin stops answering

Each call has a time limit: 10 s for actions, `/runs/{run_id}/done` and `/events`, 60 s for tools, 30 s for
`/x`. A tool answer is capped at 1 MB and an `/x` body at 8 MB.

When a container leaves 3 calls in a row unanswered (it refused the connection or ran out of
time), Loop stops calling it for 30 s and fails those calls at once: `/x` and actions with `503`,
tool calls with an `is_error` result the agent sees. After that, one call tries the container
again; an answer, of any status, closes the breaker. A container that restarts starts afresh.

## Plugin → Loop: Plugin API v1

Calls go to `$LOOP_API_URL/api/plugin/v1/...` with `Authorization: Bearer <contents of
LOOP_PLUGIN_TOKEN_FILE>`. The token is held to the plugin's channel, so no route takes a
channel id. A route needing a capability the owner didn't approve answers `403`.

| Route | Capability | Answer |
|---|---|---|
| `GET /channel` | | `{"id", "name", "dir_path", "platform", "model", "effort", "settings"}`; `settings` are the plugin's settings in the channel's project |
| `GET /channel/files?path=&start=&end=` | `files.read` | `{"path", "content", "start", "end", "total_lines"}`; lines are 1-based and inclusive, both optional (without them `content` is the whole file); `.git/` is refused, and so is a file the channel's agents may not read under agentgate (`403`); `404` if missing, `400` for a directory or a range past the end, `413` over the size limit |
| `GET /channel/tree?glob=` | `files.read` | `{"files": ["a/b.go", …], "truncated": true}`: the files git tracks (or every file outside `.git/` without git), leaving out files agentgate keeps from the channel's agents, optionally filtered by a glob matched against the path or its base name; at most 50,000, with `truncated` set when there were more |
| `GET /channel/git/recent?days=` | `git.read` | `{"commits": [{"sha", "subject", "author", "time"}], "files": [{"path", "commits"}]}`; files most-changed first; `days` defaults to 14, at most 365; the last 500 commits; `409` when the dir isn't a git repository |
| `GET /channel/quality/hotspots?limit=&offset=` | `quality.read` | a page of the latest quality scan's functions, worst first: `{"score", "raw", "total_functions", "over_threshold", "histogram", "functions": [{"path", "name", "start_line", "cyclomatic", "cognitive", "max_nesting", "param_count", "loc", "score"}], "offset", "limit", "returned"}`; `limit` defaults to 50, at most 100; `503` before the first scan |
| `POST /runs` | `agent.readonly` / `agent.write` | body `{"agent", "prompt", "model"?, "effort"?}` → `202 {"run_id", "thread_id"}`; see below |
| `POST /events` | | body `{"type", "data"}` → `204`; sent to the plugin's panels in the channel as `type` |
| `POST /notify` | `ui.notification.notify` | body `{"title", "body"}` → `204`; kept in the notification drawer of the plugin's channel, or of the thread its container serves. There's no desktop notification. Five a minute per plugin, cards included; past that `429` with `Retry-After` |
| `PUT /memory/{name}` | `memory.write` | body `{"content"}` (up to 256 KB) → `204`; writes `plugin-<id>-<name>.md` into the auto-memory dir of the channel's dir, where the channel's agents find it with `search_memory` once memory is on. `name` is lowercase letters, digits and dashes, at most 64 (`400` otherwise); `409` when the channel has no dir |
| `DELETE /memory/{name}` | `memory.write` | `204`, also when there was no such file |
| `POST /ui/commands` | `ui.run` | body `{"steps", "client_id"?, "timeout"?}` → `200 {"client_id", "results", "error"?}`; an app window runs the [UI bridge steps](api.md#post-apiuicommands) and Loop answers when it's done, up to `timeout` (a Go duration, `1m` by default, at most `10m`). The first step is `select_channel`, and each `select_channel` opens the plugin's channel or one of its threads (`403` otherwise); terminal steps work only on the agent and shell panes. `404` when no window is open, `502` when it goes away, `504` when it doesn't answer in time |
| `PUT /badge` | `ui.badge` | body `{"text", "title"?}` → `204`; shows `text` (trimmed, 1 to 16 characters on one line) on the sidebar row of the plugin's channel, or of the thread its container serves, with `title` (at most 200 characters) as its tooltip. It replaces the plugin's badge there and stays until the plugin clears it, is removed or loses `ui.badge`, the channel is deleted, or its project stops enabling the plugin; every open window drops it at once |
| `DELETE /badge` | `ui.badge` | `204`, also when there was no badge |
| `POST /cards` | `ui.notification.card` | body `{"card", "title"?, "data"?}` (up to 64 KB) → `201 {"id"}`; posts one of the manifest's cards to the notification drawer of the plugin's channel, or of the thread its container serves, with `data` for its page (see [Cards](#cards)). Shares `/notify`'s five a minute (`429`). `400` for a card the manifest doesn't declare, and outside the desktop app |

### Agent runs

`POST /runs` starts a run of one of the plugin's agents in the channel's hidden `plugin:<id>`
thread, with a fresh session. The agent gets the tools the manifest gives it as MCP tools named
`plugin_<tool>`; a read-only agent runs read-only. The plugin sends the whole prompt. When the run
ends Loop calls `POST /runs/{run_id}/done` on the plugin.

Loop keeps each run in its database until it ends, so a restart of Loop doesn't lose it: a run still
waiting in the thread's queue runs once Loop is back, and one that was going on fails, with
`/runs/{run_id}/done` sent as `failed`. One that ended well while Loop was stopping is sent as
`done` once Loop is back.

Each plugin starts at most `plugins.daily_runs` runs (50 by default) in any 24 hours, across all
channels; past that, `POST /runs` answers `429`.

## Go SDK

`github.com/radutopala/loop/pluginsdk` implements both halves of the contract for a plugin
written in Go:

- `LoadEnv` reads the container environment; `Env.Secret`, `Env.Client` and `Env.Listen` give the
  secret, a Plugin API client and the listener.
- `Server` serves `/healthz`, `/actions`, `/tools`, `/runs/{run_id}/done`, `/events` and your own
  `X` handler under `/x`, behind the secret. An action answers `202` and runs its `ActionFunc` in
  the background; a tool's error becomes a result with `is_error` set; `Events` takes each event
  while Loop waits, so hand long work to `Server.Go`. `Server.Go` runs other
  background work, which `Close` cancels.
- `Client` (the `API` interface) calls the Plugin API; a non-2xx answer is a `StatusError`, which
  `HasStatus` checks. `Hotspots` returns the worst functions, `HotspotsPage` a page of them with
  the scan's totals. `RunUI` waits as long as the command's timeout, past `Client.Timeout`.
- `ReadManifest` parses `plugin.json`, so `/healthz` can report its version.
- `Serve` serves until the context ends, then waits for requests in flight.

```go
env := pluginsdk.LoadEnv(os.Getenv)
secret, err := env.Secret()
m, err := pluginsdk.ReadManifest(pluginsdk.ManifestPath)
srv := &pluginsdk.Server{
	Secret:  secret,
	Version: m.Version,
	Actions: map[string]pluginsdk.ActionFunc{"daily": daily(env.Client())},
	X:       uiAPI,
}
defer srv.Close()
ln, err := env.Listen()
return pluginsdk.Serve(ctx, ln, srv.Handler())
```

Package `pluginsdktest` is for the plugin's tests. `NewFakeLoop` serves the Plugin API from
memory, holding the plugin to its manifest's capabilities and agents, and records the events,
notifications, runs, memory and UI commands it gets; a UI command answers that every step ran.
`Check` drives a plugin through the Loop → plugin contract: its health and version, the secret,
every action with each trigger, every tool as an agent's and as a workflow's call, and the end
of each run it started.

```go
loop := pluginsdktest.NewFakeLoop(t, pluginsdktest.Config{Manifest: m, Files: files})
srv := newServer(loop.Client())
ts := httptest.NewServer(srv.Handler())
pluginsdktest.Check(t, &pluginsdktest.Plugin{URL: ts.URL, Secret: secret}, m,
	pluginsdktest.CheckOptions{Loop: loop, Settle: srv.Wait})
```

`CheckUI` loads each panel and card of the plugin's `/loop-plugin` directory in a headless Chrome
the way Loop draws them: served with Loop's content security policy into a sandboxed frame, with
a fake bridge that answers `api.request` with `UIOptions.API`. It returns the problems it saw: a
load or inline script the policy blocked, an uncaught error or unhandled rejection, a file of the
bundle that didn't load, a failed bridge call, and a page that didn't load or take the bridge's
port within `UIOptions.Timeout` (10 seconds; `NoBridge` skips the bridge). It finds Chrome or
Chromium where chromedp looks; `Chrome.Options` takes another path, or `chromedp.NoSandbox` to
run as root in a container.

```go
problems, err := pluginsdktest.CheckUI(ctx, "../loop-plugin", pluginsdktest.UIOptions{API: uiAPI})
require.NoError(t, err)
require.Empty(t, problems)
```

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
pane talks to the plugin's container of the channel, thread or worktree thread it's in.

Once the plugin has started an agent run in the pane's channel, its panes show an **agent** label at the
right of their header. It opens the agent view over the layout, as the Learn view opens: the panel
on the left half, the run's chat on the right, and the logo on their seam, which moves while a run
runs. The label closes it again. The panel isn't reloaded on the way. The chat is read-only: only
the plugin starts runs in its thread, each in a new session of its own.

### Bridge

The TypeScript package under `app/packages/plugin-ui` wraps the bridge: `connect()` waits for the
port and returns typed `api`, `context`, `settings`, `state`, `events`, `ui` and `theme` calls,
with `applyTheme`/`syncTheme` for the theme's CSS variables and `createStorage`, a `localStorage`
stand-in kept in the panel's state. Its README covers installing and using it. The protocol below
is all it speaks, so a panel can also do without it.

On load the host posts `{"type": "loop-plugin:init"}` to the frame with a `MessagePort` in
`event.ports[0]`. The port carries JSON-RPC 2.0: the frame sends
`{"jsonrpc": "2.0", "id": 1, "method": "…", "params": {…}}` and gets back `result` or
`error: {"code", "message"}`; notifications from the host have no `id`.

| Method | Params | Result |
|---|---|---|
| `api.request` | `{"method", "path", "body"?}` | `{"status", "body"}`: the plugin's `/x/{path}` in the panel's channel, JSON bodies decoded |
| `context.get` | | `{"pluginId", "panel", "channelId", "channelName", "dirPath"}`; in a card `panel` is `""`, with `card` and `data` |
| `state.get` | | the panel's saved UI state, or `null` |
| `state.set` | `{"value"}` | `null`; up to 64 KB of JSON, kept per plugin, channel and panel |
| `theme.get` | | `{"mode": "dark"\|"light", "vars": {"--bg": "…", …}}` |
| `settings.get` | | the plugin's settings in the channel's project |
| `ui.notification.notify` | `{"title", "body"}` | `null`; kept in the notification drawer of the panel's channel, like `POST /notify`; needs `ui.notification.notify` |
| `ui.open_file` | `{"path", "line"?}` | `null`; opens a file of the channel in the editor; needs `ui.open_file` |
| `ui.copy` | `{"text"}` | `null`; puts `text`, at most 1 MB, on the clipboard; needs `ui.copy` |
| `ui.resize` | `{"height"}` | `null`; sets a card's height in pixels, kept within 80 to 900; a panel fills its pane and gets an error |
| `ui.set_title` | `{"title"}` | `null`; names the panel's pane, at most 80 characters, until the page reloads; `""` gives it back the panel's label; a card gets an error |

The theme's `vars` are `--bg`, `--surface`, `--fg`, `--muted`, `--border`, `--accent`,
`--success`, `--danger`, `--font` and `--mono`; a panel sets them on its root element.

Host notifications:

| Method | Params |
|---|---|
| `event` | `{"type", "data"}`: an event the plugin's container sent with `POST /events` |
| `theme` | as `theme.get`, when the theme changes |
| `settings` | as `settings.get`, when a config write changes them |
| `state` | the state the same panel of the channel saved with `state.set` in another pane |

### Cards

A plugin approved for `ui.notification.card` declares pages in the manifest's `ui.cards` (`id` and `entry`,
checked like a panel's) and posts one with `POST /cards`. Cards don't go in the chat, which is
for talking with the agent: they land in the notification drawer of the channel or thread, opened
from the bell at the right of the layout bar, next to Loop's own notifications about it
(scheduled runs, failed runs and scheduled scripts and plugin actions that failed, workflows). The sidebar row shows how many are unread. The drawer
shows the card under the plugin's name and the `title`; the page is in a frame, under the same
rules as a panel's. The page
gets the same bridge as a panel, with three differences: `context.get` has the card's id as
`card` and the posted `data`, `state` lasts as long as the frame, and the page sets its own
height with `ui.resize`, 160 pixels until it does. `api.request` reaches the container of the
card's channel or thread. A card of a plugin that was removed, or no longer approved for
`ui.notification.card`, shows a note in its place; removing the plugin drops its notifications.
