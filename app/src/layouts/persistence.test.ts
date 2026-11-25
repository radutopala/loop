import { beforeEach, describe, expect, it, vi } from "vitest";
import { makeLeaf } from "../splitPane/treeOps";
import {
  clearLayout,
  createDefaultLayouts,
  DEFAULT_LAYOUT_NAMES,
  deleteLayout,
  ensureDefaultLayouts,
  getLayoutNames,
  loadActiveLayoutName,
  loadChannelLayouts,
  loadLayout,
  renameLayout,
  reorderLayout,
  restoreDefaultLayouts,
  saveActiveLayout,
  saveLayout,
  saveLayoutType,
} from "./persistence";

beforeEach(() => {
  const items = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => items.set(k, v),
  });
  return () => vi.unstubAllGlobals();
});

describe("renameLayout", () => {
  it("renames a tab without panes yet", () => {
    saveLayoutType("c1", "Layout 1", "split");
    renameLayout("c1", "Layout 1", "Review 2");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "Review 2", order: ["Review 2"], types: { "Review 2": "split" } });
  });

  it("keeps a canvas tab a canvas", () => {
    saveLayoutType("c1", "Canvas 1", "canvas");
    saveLayout("c1", "Canvas 1", makeLeaf("notes", "notes"));
    renameLayout("c1", "Canvas 1", "Board");
    const ch = loadChannelLayouts("c1");
    expect(ch?.types).toEqual({ Board: "canvas" });
    expect(Object.keys(ch?.layouts ?? {})).toEqual(["Board"]);
    expect(ch?.order).toEqual(["Board"]);
  });

  it("leaves a channel without the tab as it is", () => {
    saveLayoutType("c1", "Layout 1", "split");
    renameLayout("c1", "Nope", "Other");
    renameLayout("c2", "Nope", "Other");
    expect(loadChannelLayouts("c1")?.order).toEqual(["Layout 1"]);
    expect(loadChannelLayouts("c2")).toBeNull();
  });
});

describe("loadChannelLayouts", () => {
  it("knows a canvas saved without its type", () => {
    const canvas = { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] };
    const chat = makeLeaf("chat", "chat");
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({ c1: { active: "Chat", order: ["Chat", "Canvas"], types: {}, layouts: { Chat: chat, Canvas: canvas } }, c2: { active: "Board", order: ["Board"], layouts: { Board: canvas } } }),
    );
    expect(loadChannelLayouts("c1")?.types).toEqual({ Canvas: "canvas" });
    expect(loadChannelLayouts("c2")?.types).toEqual({ Board: "canvas" });
  });
});

describe("deleteLayout", () => {
  it("forgets the tab's type", () => {
    saveLayoutType("c1", "Layout 1", "split");
    saveLayoutType("c1", "Canvas 1", "canvas");
    deleteLayout("c1", "Canvas 1");
    expect(loadChannelLayouts("c1")).toMatchObject({ order: ["Layout 1"], types: { "Layout 1": "split" } });
  });
});

const KEY = "loop-workspace-layout";
const store = (v: unknown) => localStorage.setItem(KEY, JSON.stringify(v));
const stored = () => JSON.parse(localStorage.getItem(KEY) ?? "null");
const leaf = (id: string, panel: string, flex = 1) => ({ type: "leaf", id, panel, flex });

describe("loading what's stored", () => {
  it.each([
    ["nothing", null],
    ["junk", "{not json"],
    ["a non-object", "42"],
    ["null", "null"],
  ])("reads %s as no layouts", (_, raw) => {
    if (raw !== null) localStorage.setItem(KEY, raw);
    expect(loadChannelLayouts("c1")).toBeNull();
    expect(loadLayout("c1")).toBeNull();
    expect(loadActiveLayoutName("c1")).toBeNull();
    expect(getLayoutNames("c1")).toEqual([]);
  });

  it("drops the old single-tree format and derives a missing order", () => {
    store({ old: leaf("chat", "chat"), c1: { active: "A", layouts: { A: leaf("a", "chat"), B: leaf("b", "git") } }, c2: null, c3: { active: "X" } });
    expect(loadChannelLayouts("old")).toBeNull();
    expect(loadChannelLayouts("c1")).toMatchObject({ order: ["A", "B"], types: {} });
    expect(loadChannelLayouts("c2")).toBeNull();
    expect(loadChannelLayouts("c3")).toEqual({ active: "X" });
  });

  it("reads the active layout and its name", () => {
    store({ c1: { active: "A", order: ["A"], types: {}, layouts: { A: leaf("a", "chat") } }, c2: { active: "Gone", order: [], types: {}, layouts: {} } });
    expect(loadLayout("c1")).toEqual(leaf("a", "chat"));
    expect(loadActiveLayoutName("c1")).toBe("A");
    expect(getLayoutNames("c1")).toEqual(["A"]);
    expect(loadLayout("c2")).toBeNull();
  });

  it("survives a storage that can't be written", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => null,
      setItem: () => {
        throw new Error("quota");
      },
    });
    expect(() => saveLayout("c1", "A", makeLeaf("a", "chat"))).not.toThrow();
  });
});

describe("saving", () => {
  it("adds a layout once to the order and makes it active", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    saveLayout("c1", "B", makeLeaf("b", "git"));
    saveLayout("c1", "A", makeLeaf("a2", "chat"));
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "A", order: ["A", "B"], layouts: { A: { id: "a2" } } });
    saveLayoutType("c1", "B", "canvas");
    saveLayoutType("c1", "C", "split");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "C", order: ["A", "B", "C"], types: { B: "canvas", C: "split" } });
  });

  it("switches the active layout of a known channel only", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    saveLayout("c1", "B", makeLeaf("b", "git"));
    saveActiveLayout("c1", "A");
    saveActiveLayout("c2", "A");
    expect(loadActiveLayoutName("c1")).toBe("A");
    expect(loadChannelLayouts("c2")).toBeNull();
  });

  it("clears a layout's panes but keeps its tab", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    clearLayout("c1", "A");
    clearLayout("c2", "A");
    expect(loadChannelLayouts("c1")).toMatchObject({ order: ["A"], layouts: {} });
    expect(loadChannelLayouts("c2")).toBeNull();
  });
});

describe("deleteLayout", () => {
  it("does nothing for an unknown channel", () => {
    deleteLayout("c1", "A");
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("remembers a removed default once and moves the active tab to the first left", () => {
    saveLayout("c1", "Custom", makeLeaf("x", "chat"));
    saveLayout("c1", "Git", makeLeaf("git", "git"));
    saveLayout("c1", "Chat", makeLeaf("chat", "chat"));
    deleteLayout("c1", "Chat");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "Custom", order: ["Custom", "Git"], removed: ["Chat"] });
    saveLayout("c1", "Chat", makeLeaf("chat", "chat"));
    deleteLayout("c1", "Chat");
    deleteLayout("c1", "Custom");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "Git", order: ["Git"], removed: ["Chat"] });
  });

  it("keeps the active tab when another goes", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    saveLayout("c1", "B", makeLeaf("b", "git"));
    deleteLayout("c1", "A");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "B", order: ["B"] });
  });

  it("forgets the channel once its last tab goes", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    deleteLayout("c1", "A");
    expect(loadChannelLayouts("c1")).toBeNull();
  });
});

describe("renameLayout to the same name", () => {
  it("changes nothing", () => {
    saveLayout("c1", "A", makeLeaf("a", "chat"));
    saveLayout("c1", "B", makeLeaf("b", "chat"));
    renameLayout("c1", "A", "A");
    renameLayout("c1", "A", "Z");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "B", order: ["Z", "B"] });
  });
});

describe("reorderLayout", () => {
  beforeEach(() => {
    for (const n of ["A", "B", "C"]) saveLayout("c1", n, makeLeaf(n, "chat"));
  });

  it("moves a tab", () => {
    reorderLayout("c1", 0, 2);
    expect(getLayoutNames("c1")).toEqual(["B", "C", "A"]);
  });

  it.each([
    ["the same index", 1, 1],
    ["a negative from", -1, 0],
    ["a from past the end", 3, 0],
    ["a negative to", 0, -1],
    ["a to past the end", 0, 3],
  ])("ignores %s", (_, from, to) => {
    reorderLayout("c1", from, to);
    reorderLayout("c2", 0, 1);
    expect(getLayoutNames("c1")).toEqual(["A", "B", "C"]);
    expect(loadChannelLayouts("c2")).toBeNull();
  });
});

describe("ensureDefaultLayouts", () => {
  it("creates and stores the defaults for a new channel", () => {
    const ch = ensureDefaultLayouts("c1");
    expect(ch).toEqual(createDefaultLayouts());
    expect(stored().c1).toEqual(createDefaultLayouts());
  });

  it("leaves an up-to-date channel as it is", () => {
    ensureDefaultLayouts("c1");
    const setItem = vi.spyOn(localStorage, "setItem");
    expect(ensureDefaultLayouts("c1")).toEqual(createDefaultLayouts());
    expect(setItem).not.toHaveBeenCalled();
  });

  it("adds missing defaults, skips removed ones, and puts custom tabs last", () => {
    const d = createDefaultLayouts();
    store({ c1: { ...d, order: ["Mine", "Chat", "Git"], layouts: { Chat: d.layouts.Chat, Mine: leaf("m", "chat") }, types: {}, removed: ["Git", "Review"] } });
    const ch = ensureDefaultLayouts("c1");
    const want = DEFAULT_LAYOUT_NAMES.filter((n) => n !== "Git" && n !== "Review");
    expect(ch.order).toEqual([...want, "Mine"]);
    expect(ch.layouts.Git).toBeUndefined();
    expect(ch.layouts.Review).toBeUndefined();
    expect(ch.layouts.Memory).toEqual(d.layouts.Memory);
    expect(ch.types).toEqual({ Canvas: "canvas" });
    expect(stored().c1.order).toEqual(ch.order);
  });

  it("puts a default tab missing only from the order back in it", () => {
    const d = createDefaultLayouts();
    store({ c1: { ...d, order: d.order.filter((n) => n !== "Terminal") } });
    expect(ensureDefaultLayouts("c1").order).toEqual([...DEFAULT_LAYOUT_NAMES]);
  });
});

describe("restoreDefaultLayouts", () => {
  it("hands back fresh defaults for an unknown channel", () => {
    expect(restoreDefaultLayouts("c1")).toEqual(createDefaultLayouts());
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("brings back removed defaults in their place", () => {
    const d = createDefaultLayouts();
    store({
      c1: {
        active: "Chat",
        order: ["Editor", "Mine"],
        types: {},
        layouts: { Editor: d.layouts.Editor, Mine: leaf("m", "chat"), Git: leaf("g", "git") },
        removed: ["Chat", "Git", "Review", "Terminal"],
      },
    });
    const ch = restoreDefaultLayouts("c1");
    expect(ch.order).toEqual(["Chat", "Editor", "Terminal", "Git", "Review", "Mine"]);
    expect(ch.layouts.Chat).toEqual(d.layouts.Chat);
    expect(ch.layouts.Git).toEqual(leaf("g", "git"));
    expect(ch.layouts.Terminal).toBeUndefined();
    expect(ch.removed).toEqual([]);
    expect(stored().c1.removed).toEqual([]);
  });

  it("appends a removed default when nothing comes after it", () => {
    store({ c1: { active: "Chat", order: ["Chat"], types: {}, layouts: {}, removed: ["Review", "Chat"] } });
    expect(restoreDefaultLayouts("c1").order).toEqual(["Chat", "Review"]);
  });

  it("clears a channel without removed tabs", () => {
    store({ c1: { active: "Chat", order: ["Chat"], types: {}, layouts: {} } });
    expect(restoreDefaultLayouts("c1")).toMatchObject({ order: ["Chat"], removed: [] });
  });
});

describe("migrations", () => {
  const migrate = (ch: object) => {
    store({ c1: ch });
    return ensureDefaultLayouts("c1");
  };
  const defaults = createDefaultLayouts();

  it("drops the old Default layout and renames Browser and Diff", () => {
    const ch = migrate({
      active: "Default",
      order: ["Default", "Browser", "Diff", "Mine"],
      layouts: { Default: leaf("x", "chat"), Browser: leaf("b", "browser"), Diff: leaf("diff", "diff"), Mine: leaf("m", "chat") },
      removed: ["Browser", "Diff", "Swarm"],
    });
    expect(ch.active).toBe("Chat");
    expect(ch.layouts.Default).toBeUndefined();
    expect(ch.removed).toEqual(["Browser Chat", "Git", "Swarm"]);
    // The renamed layouts are kept, but a removed tab stays out of the order.
    expect(ch.layouts.Git).toEqual(leaf("git", "git"));
    expect(ch.order).not.toContain("Browser Chat");
    expect(ch.order).not.toContain("Git");
    expect(ch.order.at(-1)).toBe("Mine");
    expect(ch.version).toBe(13);
  });

  it("moves Browser and Diff layouts to their new names and keeps them active", () => {
    const ch = migrate({ active: "Browser", order: ["Browser", "Diff"], layouts: { Browser: leaf("browser", "browser"), Diff: leaf("diff", "diff") } });
    expect(ch.active).toBe("Browser Chat");
    // Browser Chat is refreshed from defaults by v13; Git keeps the old Diff pane.
    expect(ch.layouts["Browser Chat"]).toEqual(defaults.layouts["Browser Chat"]);
    expect(ch.layouts.Git).toEqual(leaf("git", "git"));
    expect(ch.layouts.Browser).toBeUndefined();
    expect(ch.layouts.Diff).toBeUndefined();
    expect(migrate({ active: "Diff", order: ["Diff"], layouts: { Diff: leaf("d", "chat") } }).active).toBe("Git");
  });

  it("keeps an existing Browser Chat or Git over the old one", () => {
    const ch = migrate({
      active: "Mine",
      order: ["Browser", "Mine"],
      layouts: { Browser: leaf("old", "chat"), "Browser Chat": leaf("new", "chat"), Diff: leaf("old", "chat"), Git: leaf("new", "git"), Mine: leaf("m", "chat") },
    });
    expect(ch.layouts.Git).toEqual(leaf("new", "git"));
    expect(ch.active).toBe("Mine");
  });

  it("fixes canvas layouts saved without tiles", () => {
    const ch = migrate({
      active: "Chat",
      order: ["Canvas", "Board"],
      types: { Canvas: "canvas", Board: "canvas", Split: "split" },
      layouts: { Canvas: { type: "canvas" }, Board: { type: "canvas" }, Split: leaf("s", "chat") },
    });
    expect(ch.layouts.Canvas).toEqual(defaults.layouts.Canvas);
    expect(ch.layouts.Board).toEqual({ type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] });
    expect(ch.layouts.Split).toEqual(leaf("s", "chat"));
  });

  it("gives a channel without types the default ones", () => {
    store({ c1: { active: "Chat", order: [], layouts: {} } });
    expect(ensureDefaultLayouts("c1").types).toEqual({ Canvas: "canvas" });
  });

  it("renames browser, diff, agent and shell panes in trees and canvases", () => {
    const ch = migrate({
      active: "Mine",
      order: ["Mine", "Board"],
      types: { Board: "canvas" },
      layouts: {
        Empty: null,
        Mine: {
          type: "split",
          direction: "horizontal",
          flex: 1,
          children: [
            leaf("browser", "browser"),
            leaf("b2", "browser"),
            leaf("diff", "diff"),
            leaf("d2", "diff"),
            leaf("agent-0", "agent"),
            leaf("a", "agent"),
            leaf("shell-0", "shell"),
            leaf("s", "shell"),
            leaf("c", "chat"),
          ],
        },
        Board: {
          type: "canvas",
          viewport: { x: 0, y: 0, zoom: 1 },
          tiles: [
            { id: "browser", panel: "browser" },
            { id: "b2", panel: "browser" },
            { id: "diff", panel: "diff" },
            { id: "d2", panel: "diff" },
            { id: "agent-1", panel: "agent" },
            { id: 3, panel: "agent" },
            { id: "shell-1", panel: "shell" },
            { panel: "shell" },
            { id: "n", panel: "notes" },
          ],
        },
      },
    });
    const mine = ch.layouts.Mine as { children: { id: string; panel: string }[] };
    expect(mine.children.map((c) => `${c.id}:${c.panel}`)).toEqual([
      "docker-browser:docker-browser",
      "b2:docker-browser",
      "git:git",
      "d2:git",
      "docker-agent-0:docker-agent",
      "a:docker-agent",
      "host-shell-0:host-shell",
      "s:host-shell",
      "c:chat",
    ]);
    const board = ch.layouts.Board as { tiles: { id?: unknown; panel: string }[] };
    expect(board.tiles.map((t) => `${t.id}:${t.panel}`)).toEqual([
      "docker-browser:docker-browser",
      "b2:docker-browser",
      "git:git",
      "d2:git",
      "docker-agent-1:docker-agent",
      "3:docker-agent",
      "host-shell-1:host-shell",
      "undefined:host-shell",
      "n:notes",
    ]);
    expect(ch.layouts.Empty).toBeNull();
  });

  it("puts a file tree beside editors and refreshes the stock layouts", () => {
    const ch = migrate({
      active: "Chat",
      order: ["Chat", "Editor", "Mine", "Tree"],
      types: { "Browser Chat": "canvas" },
      layouts: {
        Chat: leaf("chat", "chat"),
        Editor: leaf("editor", "editor"),
        "Browser Chat": { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] },
        Mine: { type: "split", direction: "vertical", flex: 1, children: [leaf("editor", "editor", 40), leaf("c", "chat", 60)] },
        Tree: { type: "split", direction: "vertical", flex: 1, children: [leaf("file-tree", "file-tree"), leaf("editor", "editor")] },
      },
    });
    expect(ch.layouts.Chat).toEqual(defaults.layouts.Chat);
    expect(ch.layouts.Editor).toEqual(defaults.layouts.Editor);
    expect(ch.layouts["Browser Chat"]).toEqual({ type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] });
    expect(ch.layouts.Mine).toEqual({
      type: "split",
      direction: "vertical",
      flex: 1,
      children: [
        {
          type: "split",
          direction: "horizontal",
          flex: 40,
          children: [leaf("file-tree", "file-tree", 25), leaf("editor", "editor", 75)],
        },
        leaf("c", "chat", 60),
      ],
    });
    expect(ch.layouts.Tree).toEqual({ type: "split", direction: "vertical", flex: 1, children: [leaf("file-tree", "file-tree"), leaf("editor", "editor")] });
  });

  it("renames a Browser tab without panes and keeps another tab active", () => {
    const ch = migrate({ active: "Mine", order: ["Default", "Browser", "Mine"], layouts: { Default: leaf("x", "chat"), Mine: leaf("m", "chat") }, removed: ["Editor"] });
    expect(ch.active).toBe("Mine");
    expect(ch.layouts.Default).toBeUndefined();
    expect(ch.order).toContain("Browser Chat");
  });

  it("leaves out an Editor the user removed once it's past v6", () => {
    const ch = migrate({ active: "Mine", order: ["Mine"], version: 11, types: {}, layouts: { Mine: leaf("m", "chat") }, removed: ["Editor"] });
    expect(ch.layouts.Editor).toBeUndefined();
  });

  it("runs only the migrations a channel hasn't had", () => {
    const ch = migrate({ active: "Mine", order: ["Mine"], version: 12, types: {}, layouts: { Mine: leaf("agent-0", "agent") } });
    expect(ch.layouts.Mine).toEqual(leaf("agent-0", "agent"));
    expect(ch.layouts.Editor).toEqual(defaults.layouts.Editor);
  });
});
