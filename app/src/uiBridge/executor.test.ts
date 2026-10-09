import { beforeEach, describe, expect, it } from "vitest";
import type { SessionStatus } from "../types";
import type { PanelType } from "../types/panels";
import { INPUT_QUIET_MS, MAX_LINES, runSteps, WAIT_QUIET_MS } from "./executor";
import type { PaneInfo, PaneOptions, PlaygroundPick, TerminalInput, UiHost, UiStep, WorkspaceController } from "./types";

/** A workspace that keeps its panes in a list. */
class FakeWorkspace implements WorkspaceController {
  tab = "Chat";
  tabs = ["Chat", "Git"];
  canvas = false;
  panes: PaneInfo[] = [{ id: "chat", panel: "chat" }];
  calls: string[] = [];
  next = 0;
  fail = "";
  maximized: string | null = null;
  opts: (PaneOptions | undefined)[] = [];

  view() {
    return { tab: this.tab, tabs: this.tabs, canvas: this.canvas, panes: this.panes };
  }
  maximize(id: string | null) {
    this.calls.push(`maximize ${id ?? "-"}`);
    this.maximized = id;
  }
  setTab(name: string) {
    this.calls.push(`setTab ${name}`);
    this.tab = name;
  }
  replacePane(id: string, panel: PanelType, opts?: PaneOptions) {
    if (this.fail) throw new Error(this.fail);
    this.opts.push(opts);
    const openMode = opts?.openMode;
    const pane = { id: `${panel}-${this.next++}`, panel };
    this.panes = this.panes.map((p) => (p.id === id ? pane : p));
    this.calls.push(`replace ${id} ${panel} ${openMode ?? ""}`.trim());
    return pane.id;
  }
  addPane(panel: PanelType, nextTo: string | undefined, direction: string, before: boolean, opts?: PaneOptions) {
    if (this.fail) throw new Error(this.fail);
    this.opts.push(opts);
    const openMode = opts?.openMode;
    const pane = { id: `${panel}-${this.next++}`, panel };
    this.panes = [...this.panes, pane];
    this.calls.push(`add ${panel} ${nextTo ?? "-"} ${direction}${before ? " before" : ""} ${openMode ?? ""}`.trim());
    return pane.id;
  }
  removePane(id: string) {
    if (this.fail) throw new Error(this.fail);
    this.panes = this.panes.filter((p) => p.id !== id);
    this.calls.push(`remove ${id}`);
  }
  createTab(name?: string) {
    const tab = name ?? `Layout ${this.tabs.length + 1}`;
    this.calls.push(`createTab ${tab}`);
    this.tabs = [...this.tabs, tab];
    this.tab = tab;
    this.panes = [];
    return tab;
  }
  renameTab(name: string, newName: string) {
    this.calls.push(`renameTab ${name} ${newName}`);
    this.tabs = this.tabs.map((t) => (t === name ? newName : t));
    if (this.tab === name) this.tab = newName;
  }
  removeTab(name: string) {
    this.calls.push(`removeTab ${name}`);
    this.tabs = this.tabs.filter((t) => t !== name);
    if (this.tab === name) this.tab = this.tabs[0] as string;
  }
}

class FakeTerminal implements TerminalInput {
  sent: string[] = [];
  text = "";
  reads: number[] = [];
  constructor(
    public kind: "agent" | "shell",
    public st: SessionStatus = "running",
    public outputAt = 1,
  ) {}
  status() {
    return this.st;
  }
  lastOutputAt() {
    return this.outputAt;
  }
  busy() {
    return false;
  }
  read(lines: number) {
    this.reads.push(lines);
    return this.text;
  }
  send(data: string) {
    this.sent.push(data);
  }
}

/** A host with a clock that moves on only when steps sleep. */
class FakeHost implements UiHost {
  clock = 10_000;
  selected: string | null = "c1";
  workspaces = new Map<string, FakeWorkspace>();
  terminals = new Map<string, FakeTerminal>();
  channels = new Set(["c1", "c2"]);
  onSleep: (() => void) | null = null;
  selected_: string[] = [];
  opened: string[] = [];
  files = new Set(["main.go"]);
  pgs: PlaygroundPick[] = [
    { name: "board", scope: "global" },
    { name: "both", scope: "global" },
    { name: "both", scope: "project" },
  ];

  selectedChannelId() {
    return this.selected;
  }
  async selectChannel(id: string) {
    if (!this.channels.has(id)) throw new Error(`no channel ${id}`);
    this.selected_.push(id);
    this.selected = id;
  }
  workspace(channelId: string) {
    return this.workspaces.get(channelId);
  }
  terminal(channelId: string, paneId: string) {
    return this.terminals.get(`${channelId}:${paneId}`);
  }
  async openFile(channelId: string, path: string, line?: number) {
    if (!this.files.has(path)) throw new Error(`no file "${path}" in the channel's roots`);
    this.opened.push(`${channelId} ${path}${line ? `:${line}` : ""}`);
  }
  async playgrounds() {
    return this.pgs;
  }
  now() {
    return this.clock;
  }
  async sleep(ms: number) {
    this.clock += ms;
    this.onSleep?.();
  }
}

let host: FakeHost;
let ws: FakeWorkspace;

beforeEach(() => {
  host = new FakeHost();
  ws = new FakeWorkspace();
  host.workspaces.set("c1", ws);
});

const run = (steps: UiStep[], timeoutMs = 5_000) => runSteps(steps, host, timeoutMs);

describe("runSteps", () => {
  it("runs steps in order and reports the panes they made", async () => {
    const results = await run([
      { op: "set_tab", tab: "Git" },
      { op: "replace_pane", pane: "chat", panel: "docker-agent", open_mode: "fresh" },
      { op: "add_pane", panel: "playground", next_to: "docker-agent", direction: "vertical" },
      { op: "add_pane", panel: "notes" },
      { op: "add_pane", panel: "git", next_to: "docker-agent", side: "before" },
      { op: "remove_pane", pane: "notes" },
    ]);
    expect(results).toEqual([
      { op: "set_tab", ok: true },
      { op: "replace_pane", ok: true, pane: "docker-agent-0" },
      { op: "add_pane", ok: true, pane: "playground-1" },
      { op: "add_pane", ok: true, pane: "notes-2" },
      { op: "add_pane", ok: true, pane: "git-3" },
      { op: "remove_pane", ok: true },
    ]);
    expect(ws.calls).toEqual([
      "setTab Git",
      "replace chat docker-agent fresh",
      "add playground docker-agent-0 vertical",
      "add notes - horizontal",
      "add git docker-agent-0 horizontal before",
      "remove notes-2",
    ]);
  });

  it("stops at the first step that fails", async () => {
    const results = await run([{ op: "nope" }, { op: "set_tab", tab: "Git" }]);
    expect(results).toEqual([{ op: "nope", ok: false, error: 'unknown op "nope"' }]);
    expect(ws.calls).toEqual([]);
  });

  it.each<{ name: string; step: UiStep; error: string }>([
    { name: "set_tab without a tab", step: { op: "set_tab" }, error: "tab is required" },
    { name: "set_tab to no such tab", step: { op: "set_tab", tab: "Nope" }, error: 'no tab "Nope"; tabs: Chat, Git' },
    { name: "create_tab with a blank name", step: { op: "create_tab", tab: "  " }, error: "tab can't be empty" },
    { name: "create_tab with a taken name", step: { op: "create_tab", tab: "Git" }, error: 'there\'s already a tab "Git"' },
    { name: "rename_tab without a tab", step: { op: "rename_tab", name: "X" }, error: "tab is required" },
    { name: "rename_tab without a name", step: { op: "rename_tab", tab: "Git", name: " " }, error: "name is required" },
    { name: "rename_tab of no such tab", step: { op: "rename_tab", tab: "Nope", name: "X" }, error: 'no tab "Nope"; tabs: Chat, Git' },
    { name: "rename_tab to a taken name", step: { op: "rename_tab", tab: "Git", name: "Chat" }, error: 'there\'s already a tab "Chat"' },
    { name: "remove_tab without a tab", step: { op: "remove_tab" }, error: "tab is required" },
    { name: "remove_tab of no such tab", step: { op: "remove_tab", tab: "Nope" }, error: 'no tab "Nope"; tabs: Chat, Git' },
    { name: "a pane step without a pane", step: { op: "remove_pane" }, error: "pane is required" },
    { name: "no such pane", step: { op: "remove_pane", pane: "git" }, error: 'no pane "git" in the open tab; panes: chat' },
    { name: "without a panel", step: { op: "add_pane" }, error: "panel is required" },
    { name: "an unknown panel", step: { op: "add_pane", panel: "clock" }, error: 'unknown panel "clock"' },
    { name: "an unknown open mode", step: { op: "add_pane", panel: "docker-agent", open_mode: "new" }, error: 'unknown open_mode "new"; use resume, fork or fresh' },
    { name: "an unknown side", step: { op: "add_pane", panel: "notes", side: "left" }, error: 'unknown side "left"; use before or after' },
    { name: "an unknown direction", step: { op: "add_pane", panel: "notes", direction: "left" }, error: 'unknown direction "left"; use horizontal or vertical' },
    { name: "a missing next_to", step: { op: "add_pane", panel: "notes", next_to: "git" }, error: 'no pane "git" in the open tab; panes: chat' },
    { name: "select_channel without an id", step: { op: "select_channel" }, error: "channel_id is required" },
    { name: "select_channel to no such channel", step: { op: "select_channel", channel_id: "c9" }, error: "no channel c9" },
  ])("refuses $name", async ({ step, error }) => {
    expect(await run([step])).toEqual([{ op: step.op, ok: false, error }]);
  });

  it("reports what the workspace refuses", async () => {
    ws.fail = "the tab can't have another chat pane";
    for (const step of [
      { op: "replace_pane", pane: "chat", panel: "chat" },
      { op: "add_pane", panel: "chat" },
      { op: "remove_pane", pane: "chat" },
    ]) {
      expect(await run([step])).toEqual([{ op: step.op, ok: false, error: ws.fail }]);
    }
  });

  it("reports a thrown non-Error", async () => {
    ws.removePane = () => {
      throw "boom";
    };
    expect(await run([{ op: "remove_pane", pane: "chat" }])).toEqual([{ op: "remove_pane", ok: false, error: "boom" }]);
    host.selectChannel = () => Promise.reject("gone");
    expect(await run([{ op: "select_channel", channel_id: "c2" }])).toEqual([{ op: "select_channel", ok: false, error: "gone" }]);
  });

  it("needs an open channel", async () => {
    host.selected = null;
    expect(await run([{ op: "set_tab", tab: "Git" }])).toEqual([{ op: "set_tab", ok: false, error: "no channel is open; select_channel first" }]);
    host.selected = "c2"; // selected, its workspace not mounted
    expect(await run([{ op: "add_pane", panel: "notes" }])).toEqual([{ op: "add_pane", ok: false, error: "no channel is open; select_channel first" }]);
  });

  it("does pane steps only on a split tab", async () => {
    ws.canvas = true;
    expect(await run([{ op: "add_pane", panel: "notes" }])).toEqual([{ op: "add_pane", ok: false, error: "the open tab is a canvas; pane steps work on split tabs" }]);
  });

  it("leaves the open tab alone", async () => {
    expect(await run([{ op: "set_tab", tab: "Chat" }])).toEqual([{ op: "set_tab", ok: true }]);
    expect(ws.calls).toEqual([]);
  });

  it("waits for a tab to open", async () => {
    ws.setTab = (name) => {
      host.onSleep = () => {
        ws.tab = name;
      };
    };
    expect(await run([{ op: "set_tab", tab: "Git" }])).toEqual([{ op: "set_tab", ok: true }]);
  });

  it("creates a named tab and adds a pane to it", async () => {
    const results = await run([
      { op: "create_tab", tab: " Bridge " },
      { op: "add_pane", panel: "notes" },
    ]);
    expect(results).toEqual([
      { op: "create_tab", ok: true, tab: "Bridge" },
      { op: "add_pane", ok: true, pane: "notes-0" },
    ]);
    expect(ws.calls).toEqual(["createTab Bridge", "add notes - horizontal"]);
  });

  it("creates a tab with the next name and waits for it to open", async () => {
    ws.createTab = (name) => {
      host.onSleep = () => {
        ws.tab = "Layout 3";
      };
      expect(name).toBeUndefined();
      return "Layout 3";
    };
    expect(await run([{ op: "create_tab" }])).toEqual([{ op: "create_tab", ok: true, tab: "Layout 3" }]);
  });

  it("renames a tab", async () => {
    expect(await run([{ op: "rename_tab", tab: "Chat", name: " Talk " }])).toEqual([{ op: "rename_tab", ok: true, tab: "Talk" }]);
    expect(ws.calls).toEqual(["renameTab Chat Talk"]);
    expect(ws.view()).toMatchObject({ tab: "Talk", tabs: ["Talk", "Git"] });
  });

  it("leaves a tab renamed to its own name", async () => {
    expect(await run([{ op: "rename_tab", tab: "Git", name: "Git" }])).toEqual([{ op: "rename_tab", ok: true, tab: "Git" }]);
    expect(ws.calls).toEqual([]);
  });

  it("waits for a renamed tab", async () => {
    ws.renameTab = (name, newName) => {
      host.onSleep = () => {
        ws.tabs = ws.tabs.map((t) => (t === name ? newName : t));
      };
    };
    expect(await run([{ op: "rename_tab", tab: "Git", name: "Diff" }])).toEqual([{ op: "rename_tab", ok: true, tab: "Diff" }]);
  });

  it("removes a tab and waits for it to close", async () => {
    ws.removeTab = (name) => {
      host.onSleep = () => {
        ws.tabs = ws.tabs.filter((t) => t !== name);
      };
    };
    expect(await run([{ op: "remove_tab", tab: "Git" }])).toEqual([{ op: "remove_tab", ok: true }]);
    expect(ws.tabs).toEqual(["Chat"]);
  });

  it("doesn't remove the last tab", async () => {
    ws.tabs = ["Chat"];
    expect(await run([{ op: "remove_tab", tab: "Chat" }])).toEqual([{ op: "remove_tab", ok: false, error: "the last tab can't be removed" }]);
    expect(ws.calls).toEqual([]);
  });

  it("selects a channel and waits for its workspace", async () => {
    const ws2 = new FakeWorkspace();
    host.onSleep = () => host.workspaces.set("c2", ws2);
    const results = await run([
      { op: "select_channel", channel_id: "c2" },
      { op: "add_pane", panel: "notes" },
    ]);
    expect(results.map((r) => r.ok)).toEqual([true, true]);
    expect(host.selected_).toEqual(["c2"]);
    expect(ws2.calls).toEqual(["add notes - horizontal"]);
    expect(ws.calls).toEqual([]);
  });

  it("doesn't select the open channel again", async () => {
    expect(await run([{ op: "select_channel", channel_id: "c1" }])).toEqual([{ op: "select_channel", ok: true }]);
    expect(host.selected_).toEqual([]);
  });

  it("gives up on a channel that doesn't open", async () => {
    const started = host.clock;
    expect(await run([{ op: "select_channel", channel_id: "c2" }], 1_000)).toEqual([{ op: "select_channel", ok: false, error: "timed out waiting for channel c2 to open" }]);
    expect(host.clock - started).toBe(1_000);
  });

  it("uses a minute when there's no timeout", async () => {
    const started = host.clock;
    await runSteps([{ op: "select_channel", channel_id: "c2" }], host);
    expect(host.clock - started).toBe(60_000);
  });
});

describe("send_input", () => {
  beforeEach(() => {
    ws.panes = [
      { id: "chat", panel: "chat" },
      { id: "host-shell-0", panel: "host-shell" },
      { id: "docker-agent-1", panel: "docker-agent" },
      { id: "docker-shell-2", panel: "docker-shell" },
    ];
  });

  it("pastes into the agent and submits", async () => {
    const t = new FakeTerminal("agent");
    host.terminals.set("c1:docker-agent-1", t);
    expect(await run([{ op: "send_input", pane: "docker-agent", text: "hi\nthere" }])).toEqual([{ op: "send_input", ok: true, pane: "docker-agent-1" }]);
    expect(await run([{ op: "send_input", pane: "docker-agent-1", text: "draft", submit: false }])).toEqual([{ op: "send_input", ok: true, pane: "docker-agent-1" }]);
    expect(t.sent).toEqual(["\x1b[200~hi\nthere\x1b[201~\r", "\x1b[200~draft\x1b[201~"]);
  });

  it("types into a container shell", async () => {
    const t = new FakeTerminal("shell");
    host.terminals.set("c1:docker-shell-2", t);
    await run([{ op: "send_input", pane: "docker-shell", text: "ls" }]);
    await run([{ op: "send_input", pane: "docker-shell", text: "cd", submit: false }]);
    expect(t.sent).toEqual(["ls\n", "cd"]);
  });

  it.each([
    { pane: "host-shell", panel: "host-shell" },
    { pane: "host-shell-0", panel: "host-shell" },
    { pane: "chat", panel: "chat" },
  ])("never types into $pane", async ({ pane, panel }) => {
    // Even with a terminal there, which a host shell never registers.
    const t = new FakeTerminal("shell");
    host.terminals.set(`c1:${pane}`, t);
    expect(await run([{ op: "send_input", pane, text: "ls" }])).toEqual([{ op: "send_input", ok: false, error: `send_input only types into docker-agent and docker-shell panes, not ${panel}` }]);
    expect(t.sent).toEqual([]);
  });

  it("needs text", async () => {
    expect(await run([{ op: "send_input", pane: "docker-agent" }])).toEqual([{ op: "send_input", ok: false, error: "text is required" }]);
  });

  it("waits for a new pane's session to start and go quiet", async () => {
    const t = new FakeTerminal("agent", "connecting", 0);
    let sleeps = 0;
    host.onSleep = () => {
      sleeps++;
      if (sleeps === 2) host.terminals.set("c1:docker-agent-1", t);
      if (sleeps === 4) t.st = "running";
      if (sleeps === 6) t.outputAt = host.clock; // the TUI prints
    };
    const results = await run([{ op: "send_input", pane: "docker-agent", text: "go" }]);
    expect(results).toEqual([{ op: "send_input", ok: true, pane: "docker-agent-1" }]);
    expect(host.clock - t.outputAt).toBeGreaterThanOrEqual(INPUT_QUIET_MS);
    expect(t.sent).toEqual(["\x1b[200~go\x1b[201~\r"]);
  });

  it.each<{ status: SessionStatus; word: string }>([
    { status: "completed", word: "ended" },
    { status: "failed", word: "failed" },
  ])("fails when the session has $status", async ({ status, word }) => {
    host.terminals.set("c1:docker-agent-1", new FakeTerminal("agent", status));
    expect(await run([{ op: "send_input", pane: "docker-agent", text: "go" }])).toEqual([{ op: "send_input", ok: false, error: `pane docker-agent-1's session has ${word}` }]);
  });

  it("gives up on a pane that isn't ready", async () => {
    expect(await run([{ op: "send_input", pane: "docker-agent", text: "go" }], 500)).toEqual([
      { op: "send_input", ok: false, error: "timed out waiting for pane docker-agent-1 to be ready for input" },
    ]);
  });
});

describe("more steps", () => {
  it("maximizes a pane and puts it back", async () => {
    expect(await run([{ op: "maximize_pane", pane: "chat" }, { op: "restore_pane" }])).toEqual([
      { op: "maximize_pane", ok: true, pane: "chat" },
      { op: "restore_pane", ok: true },
    ]);
    expect(ws.calls).toEqual(["maximize chat", "maximize -"]);
  });

  it("opens a file", async () => {
    expect(
      await run([
        { op: "open_file", path: "main.go", line: 12 },
        { op: "open_file", path: "main.go" },
      ]),
    ).toEqual([
      { op: "open_file", ok: true },
      { op: "open_file", ok: true },
    ]);
    expect(host.opened).toEqual(["c1 main.go:12", "c1 main.go"]);
  });

  it.each<{ name: string; step: UiStep; error: string }>([
    { name: "open_file without a path", step: { op: "open_file" }, error: "path is required" },
    { name: "open_file at line 0", step: { op: "open_file", path: "main.go", line: 0 }, error: "line must be a whole number from 1" },
    { name: "open_file of no such file", step: { op: "open_file", path: "nope.go" }, error: `no file "nope.go" in the channel's roots` },
    { name: "item on a non-playground pane", step: { op: "add_pane", panel: "notes", item: "board" }, error: "item and scope are for playground panes" },
    { name: "scope without an item", step: { op: "add_pane", panel: "playground", scope: "global" }, error: "item is required with scope" },
    { name: "an unknown scope", step: { op: "add_pane", panel: "playground", item: "board", scope: "team" }, error: 'unknown scope "team"; use global or project' },
    { name: "no such playground", step: { op: "add_pane", panel: "playground", item: "nope" }, error: 'no playground "nope"; playgrounds: board (global), both (global), both (project)' },
    { name: "a playground in both scopes", step: { op: "replace_pane", pane: "chat", panel: "playground", item: "both" }, error: 'playground "both" is both global and project; give scope' },
  ])("refuses $name", async ({ step, error }) => {
    expect(await run([step])).toEqual([{ op: step.op, ok: false, error }]);
  });

  it("names no playgrounds when there are none", async () => {
    host.pgs = [];
    expect(await run([{ op: "add_pane", panel: "playground", item: "x" }])).toEqual([{ op: "add_pane", ok: false, error: 'no playground "x"; playgrounds: none' }]);
  });

  it("opens a playground pane on a playground", async () => {
    expect(
      await run([
        { op: "add_pane", panel: "playground", item: "board" },
        { op: "replace_pane", pane: "chat", panel: "playground", item: "both", scope: "project" },
        { op: "add_pane", panel: "docker-agent", open_mode: "fresh" },
      ]),
    ).toEqual([
      { op: "add_pane", ok: true, pane: "playground-0" },
      { op: "replace_pane", ok: true, pane: "playground-1" },
      { op: "add_pane", ok: true, pane: "docker-agent-2" },
    ]);
    expect(ws.opts).toEqual([{ playground: { name: "board", scope: "global" } }, { playground: { name: "both", scope: "project" } }, { openMode: "fresh" }]);
  });

  it.each(["maximize_pane", "restore_pane", "open_file"])("does %s only on a split tab", async (op) => {
    ws.canvas = true;
    expect(await run([{ op, pane: "chat", path: "main.go" }])).toEqual([{ op, ok: false, error: "the open tab is a canvas; pane steps work on split tabs" }]);
  });
});

describe("read_output and wait_for", () => {
  let t: FakeTerminal;
  beforeEach(() => {
    ws.panes = [
      { id: "host-shell-0", panel: "host-shell" },
      { id: "docker-agent-1", panel: "docker-agent" },
    ];
    t = new FakeTerminal("agent");
    t.text = "done";
    host.terminals.set("c1:docker-agent-1", t);
  });

  it("reads the last lines", async () => {
    expect(
      await run([
        { op: "read_output", pane: "docker-agent" },
        { op: "read_output", pane: "docker-agent", lines: 3 },
      ]),
    ).toEqual([
      { op: "read_output", ok: true, pane: "docker-agent-1", output: "done" },
      { op: "read_output", ok: true, pane: "docker-agent-1", output: "done" },
    ]);
    expect(t.reads).toEqual([50, 3]);
  });

  it.each<{ name: string; step: UiStep; error: string }>([
    { name: "a host shell", step: { op: "read_output", pane: "host-shell" }, error: "read_output only reads docker-agent and docker-shell panes, not host-shell" },
    { name: "wait_for on a host shell", step: { op: "wait_for", pane: "host-shell" }, error: "wait_for only reads docker-agent and docker-shell panes, not host-shell" },
    { name: "0 lines", step: { op: "read_output", pane: "docker-agent", lines: 0 }, error: `lines must be a whole number from 1 to ${MAX_LINES}` },
    { name: "too many lines", step: { op: "wait_for", pane: "docker-agent", lines: MAX_LINES + 1 }, error: `lines must be a whole number from 1 to ${MAX_LINES}` },
    { name: "a bad match", step: { op: "wait_for", pane: "docker-agent", match: "(" }, error: "match: Invalid regular expression: /(/m: Unterminated group" },
    { name: "a negative quiet_ms", step: { op: "wait_for", pane: "docker-agent", quiet_ms: -1 }, error: "quiet_ms must be a whole number from 0" },
  ])("refuses $name", async ({ step, error }) => {
    expect(await run([step])).toEqual([{ op: step.op, ok: false, error }]);
  });

  it("waits for a pane's session to start before reading", async () => {
    t.st = "connecting";
    host.onSleep = () => {
      t.st = "running";
    };
    expect(await run([{ op: "read_output", pane: "docker-agent" }])).toEqual([{ op: "read_output", ok: true, pane: "docker-agent-1", output: "done" }]);
    host.onSleep = null;
    t.st = "connecting";
    expect(await run([{ op: "read_output", pane: "docker-agent" }], 100)).toEqual([{ op: "read_output", ok: false, error: "timed out waiting for pane docker-agent-1 to be started" }]);
  });

  it("waits for the output to go quiet", async () => {
    t.outputAt = host.clock;
    const started = host.clock;
    expect(await run([{ op: "wait_for", pane: "docker-agent" }])).toEqual([{ op: "wait_for", ok: true, pane: "docker-agent-1", output: "done" }]);
    expect(host.clock - started).toBe(WAIT_QUIET_MS);
  });

  it("waits for what a send_input brings", async () => {
    t.outputAt = 1; // long quiet
    let sleeps = 0;
    host.onSleep = () => {
      sleeps++;
      // The reply prints well after the prompt was sent.
      if (sleeps === 10) {
        t.outputAt = host.clock;
        t.text = "> 42";
      }
    };
    const results = await run([
      { op: "send_input", pane: "docker-agent", text: "6*7" },
      { op: "wait_for", pane: "docker-agent", match: "^> \\d+$", lines: 5 },
    ]);
    expect(results).toEqual([
      { op: "send_input", ok: true, pane: "docker-agent-1" },
      { op: "wait_for", ok: true, pane: "docker-agent-1", output: "> 42" },
    ]);
    expect(sleeps).toBe(10);
  });

  it("is done when the session ends", async () => {
    t.st = "completed";
    expect(await run([{ op: "wait_for", pane: "docker-agent", match: "never" }])).toEqual([{ op: "wait_for", ok: true, pane: "docker-agent-1", output: "done" }]);
  });

  it("gives up on output that never comes", async () => {
    t.outputAt = host.clock;
    expect(await run([{ op: "wait_for", pane: "docker-agent", match: "never" }], 500)).toEqual([{ op: "wait_for", ok: false, error: "timed out waiting for pane docker-agent-1 to print /never/" }]);
    t.text = "busy";
    expect(await run([{ op: "wait_for", pane: "docker-agent", quiet_ms: 10_000 }], 500)).toEqual([{ op: "wait_for", ok: false, error: "timed out waiting for pane docker-agent-1 to be quiet" }]);
  });
});
