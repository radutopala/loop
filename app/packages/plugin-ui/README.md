# @loop/plugin-ui

A typed client for the bridge between a plugin's panel and Loop. A panel runs in a sandboxed
frame with an opaque origin; Loop hands it a `MessagePort` on load, and the bridge is JSON-RPC
2.0 over that port. This package wraps it. It has no dependencies, and it's optional: the
protocol is described in Loop's plugin docs, under Panels → Bridge.

## Install

The package isn't published. Depend on it as a local directory:

```json
{
  "dependencies": {
    "@loop/plugin-ui": "file:../path/to/loop/app/packages/plugin-ui"
  }
}
```

It ships TypeScript source (`src/index.ts`), which Vite, esbuild and other bundlers compile
along with the panel, so there's no build step for the package. `tsc` reads its types from the
same file with `moduleResolution` `bundler`, `node16` or `nodenext`.

## Use

Call `connect()` at startup, before anything awaits: Loop posts the port as the frame loads,
and a listener added later misses it.

```ts
import { connect, createStorage, syncTheme } from "@loop/plugin-ui";

const bridge = await connect();
await syncTheme(bridge); // Loop's CSS variables on <html>, now and on change

const ctx = await bridge.context.get(); // { pluginId, panel, channelId, channelName, dirPath }
const settings = await bridge.settings.get();
bridge.settings.onChange((next) => render(next));

// The plugin's own API, /x/{path}, in the panel's channel.
const items = await bridge.api.get<Item[]>("items"); // throws ApiError on non-2xx
const res = await bridge.api.request("POST", "items", { name: "a" }); // { status, body }, any status

bridge.events.on((ev) => {
  if (ev.type === "item_added") refresh();
});

await bridge.state.set({ tab: "history" }); // up to 64 KB of JSON
const saved = await bridge.state.get<{ tab: string }>();

await bridge.ui.notify("Done", "3 items added"); // needs the ui.notify capability
await bridge.ui.openFile("cmd/main.go", 12); // needs the ui.open_file capability
await bridge.ui.copy("go test ./..."); // needs the ui.copy capability
```

| | |
|---|---|
| `api.request(method, path, body?)` | `{status, body}` of `/x/{path}`; a body that isn't a string is sent as JSON |
| `api.get/post/put/patch/delete` | the answer's body, or an `ApiError {status, body}` |
| `context.get()` | where the panel is shown; in a card also its `card` and `data` |
| `settings.get()`, `settings.onChange(cb)` | the plugin's settings in the channel's project |
| `state.get()`, `state.set(value)` | small JSON UI state, kept per plugin, channel and panel |
| `events.on(cb)` | `{type, data}` the plugin's container sent with `POST /events` |
| `ui.notify(title, body?)`, `ui.openFile(path, line?)` | |
| `ui.copy(text)` | puts text on the clipboard, at most 1 MB |
| `ui.resize(height)` | a card's height in pixels, kept within 80 to 900 |
| `theme.get()`, `theme.onChange(cb)` | `{mode, vars}` |
| `call(method, params?, timeoutMs?)` | any bridge method by name |
| `close()` | fails the calls in flight and closes the port |

A failed call rejects with a `BridgeError {code, message}`; `ErrorCode` lists the codes. Calls
time out after 10 s, `api` calls after 35 s (Loop gives the container 30 s); `connect` takes
`timeoutMs` and `apiTimeoutMs` to change that.

### Theme

`applyTheme(theme, el = document.documentElement)` sets the theme's variables on `el`, its mode
as `data-theme` and the matching `color-scheme`; `syncTheme(bridge, el?)` does it now and on
every change. The variables are `--bg`, `--surface`, `--fg`, `--muted`, `--border`, `--accent`,
`--success`, `--danger`, `--font` and `--mono`:

```css
body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font-family: var(--font);
}
```

### Storage

The frame has no `localStorage`. `createStorage(bridge)` returns a stand-in for libraries that
persist through one. It reads the panel's state once, works in memory, and saves changes with
`state.set` 50 ms after the last one (`debounceMs`; `flush()` saves now). Like the state, it's
kept per plugin, channel and panel, and it takes the whole state, so don't also call
`state.set`. A `setItem` that would take it over 64 KB throws a `QuotaExceededError`, as
`localStorage` does.

```ts
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";

const storage = await createStorage(bridge);
const useStore = create(persist(() => ({ tab: "today" }), { name: "ui", storage: createJSONStorage(() => storage) }));
```

Durable data belongs in the plugin, behind its `/x` API.

### Cards

A card is a page the plugin posts to the app's notification drawer (`ui.card`). The same bridge drives it,
with these differences: `context.get()` has `panel: ""`, the card's id as `card` and the data
it was posted with as `data`; the state lasts only as long as the frame; and the card sizes
itself with `ui.resize`, while a panel fills its pane:

```ts
const { data } = await bridge.context.get<{ score: number }>();
render(data);
new ResizeObserver(() => bridge.ui.resize(document.documentElement.scrollHeight)).observe(document.body);
```

## Bundle rules

The frame loads under a strict content security policy:

- Ship everything in the bundle: no CDN scripts, web fonts or remote images. Only the plugin's
  own files, `data:` and `blob:` load.
- No inline `<script>`; use script files (Vite's default output is fine). Build with relative
  URLs (`base: "./"` in Vite).
- No `eval` or `new Function`; use precompiled or runtime-only builds of packages that need
  them.
- No `fetch` or WebSockets to anything but the bundle's own files; data goes through
  `bridge.api`.
- No `localStorage`, `IndexedDB` or cookies; use `bridge.state` or `createStorage`.
- No popups, new windows or navigation away; open the channel's files with `bridge.ui.openFile`.
  Client-side routing works with a hash or in-memory router.
