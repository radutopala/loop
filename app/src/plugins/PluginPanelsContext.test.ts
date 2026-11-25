// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPlugins, PLUGINS_CHANGED, type PluginInfo } from "../api/plugins";
import { useEventStream } from "../hooks/useEventStream";
import type { WSEvent } from "../types";
import type { PaneNode } from "../types/panels";
import {
  findPluginCard,
  findPluginPanel,
  PluginPanelsProvider,
  pluginPaneKey,
  pluginPaneOptions,
  shownPluginPanes,
  useInstalledPlugins,
  usePluginPanels,
  usePluginPanelsValue,
} from "./PluginPanelsContext";

vi.mock("../api/plugins", async (importOriginal) => ({ ...(await importOriginal<typeof import("../api/plugins")>()), fetchPlugins: vi.fn() }));
vi.mock("../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

const fetchMock = vi.mocked(fetchPlugins);
const streamMock = vi.mocked(useEventStream);

const quiz = { id: "quiz", label: "Quiz", entry: "quiz/index.html", singleton: true };
const score = { id: "score", entry: "ui/score.html" };
const cogtest: PluginInfo = {
  id: "cogtest",
  name: "Cognitive tests",
  version: "0.1.0",
  capabilities: ["ui.panel"],
  status: "installed",
  panels: [quiz, { id: "log", label: "", entry: "log.html" }],
  cards: [score],
};
const plugins: PluginInfo[] = [
  cogtest,
  { id: "bare", version: "1", capabilities: [], status: "installed", panels: [{ id: "p", label: "", entry: "p.html" }] },
  { id: "headless", version: "1", capabilities: [], status: "installed" },
];

describe("shownPluginPanes", () => {
  it.each<{ name: string; tree: PaneNode | null; want: string[] }>([
    { name: "no tree", tree: null, want: [] },
    { name: "a built-in leaf", tree: { type: "leaf", id: "chat", panel: "chat", flex: 1 }, want: [] },
    { name: "a plugin leaf without a ref", tree: { type: "leaf", id: "p", panel: "plugin", flex: 1 }, want: [] },
    {
      name: "nested plugin leaves",
      tree: {
        type: "split",
        direction: "horizontal",
        flex: 1,
        children: [
          { type: "leaf", id: "chat", panel: "chat", flex: 1 },
          { type: "leaf", id: "q", panel: "plugin", flex: 1, plugin: { id: "cogtest", panel: "quiz", label: "Quiz" } },
        ],
      },
      want: ["cogtest/quiz"],
    },
  ])("$name", ({ tree, want }) => {
    expect([...shownPluginPanes(tree)]).toEqual(want);
  });
});

describe("pluginPaneOptions", () => {
  it("lists every panel, labelled, with singletons shown disabled", () => {
    expect(pluginPaneOptions({ plugins, shown: new Set(["cogtest/quiz", "cogtest/log"]) })).toEqual([
      { key: "cogtest/quiz", ref: { id: "cogtest", panel: "quiz", label: "Quiz" }, description: "Cognitive tests plugin", disabled: true },
      { key: "cogtest/log", ref: { id: "cogtest", panel: "log", label: "Cognitive tests" }, description: "Cognitive tests plugin", disabled: false },
      { key: "bare/p", ref: { id: "bare", panel: "p", label: "bare" }, description: "bare plugin", disabled: false },
    ]);
  });

  it("leaves out plugins not enabled in the project", () => {
    const off = { ...cogtest, enabled: false };
    const on = { ...plugins[1]!, enabled: true };
    expect(pluginPaneOptions({ plugins: [off, on], shown: new Set() }).map((o) => o.key)).toEqual(["bare/p"]);
  });

  it("lists nothing while loading", () => {
    expect(pluginPaneOptions({ plugins: null, shown: new Set() })).toEqual([]);
  });
});

describe("findPluginPanel", () => {
  it.each([
    { name: "no ref", list: plugins, ref: undefined, want: null },
    { name: "loading", list: null, ref: { id: "cogtest", panel: "quiz", label: "" }, want: null },
    { name: "plugin gone", list: plugins, ref: { id: "gone", panel: "quiz", label: "" }, want: null },
    { name: "panel gone", list: plugins, ref: { id: "cogtest", panel: "gone", label: "" }, want: null },
    { name: "no panels", list: plugins, ref: { id: "headless", panel: "x", label: "" }, want: null },
    { name: "found", list: plugins, ref: { id: "cogtest", panel: "quiz", label: "" }, want: { plugin: cogtest, panel: quiz } },
  ])("$name", ({ list, ref, want }) => {
    expect(findPluginPanel(list, ref)).toEqual(want);
  });
});

describe("findPluginCard", () => {
  it.each([
    { name: "loading", list: null, plugin: "cogtest", card: "score", want: null },
    { name: "plugin gone", list: plugins, plugin: "gone", card: "score", want: null },
    { name: "card gone", list: plugins, plugin: "cogtest", card: "gone", want: null },
    { name: "no cards", list: plugins, plugin: "headless", card: "score", want: null },
    { name: "found", list: plugins, plugin: "cogtest", card: "score", want: { plugin: cogtest, card: score } },
    { name: "disabled here", list: [{ ...cogtest, enabled: false }], plugin: "cogtest", card: "score", want: null },
  ])("$name", ({ list, plugin, card, want }) => {
    expect(findPluginCard(list, plugin, card)).toEqual(want);
  });
});

it("pluginPaneKey", () => {
  expect(pluginPaneKey({ id: "a", panel: "b" })).toBe("a/b");
});

describe("usePluginPanels", () => {
  afterEach(cleanup);

  it("reads an empty value outside a provider", () => {
    const { result } = renderHook(() => usePluginPanels());
    expect(result.current.plugins).toEqual([]);
    expect([...result.current.shown]).toEqual([]);
  });

  it("reads the provider's value", () => {
    const value = { plugins, shown: new Set(["cogtest/quiz"]) };
    const { result } = renderHook(() => usePluginPanels(), { wrapper: ({ children }) => createElement(PluginPanelsProvider, { value }, children) });
    expect(result.current).toBe(value);
  });
});

describe("usePluginPanelsValue", () => {
  afterEach(cleanup);

  it("keeps the value for a new set of the same panes and makes a new one when they change", () => {
    const { result, rerender } = renderHook(({ shown }) => usePluginPanelsValue(plugins, shown), { initialProps: { shown: new Set(["b", "a"]) } });
    const first = result.current;
    expect(first.plugins).toBe(plugins);
    rerender({ shown: new Set(["a", "b"]) });
    expect(result.current).toBe(first);
    rerender({ shown: new Set(["a"]) });
    expect(result.current).not.toBe(first);
    expect([...result.current.shown]).toEqual(["a"]);
  });
});

describe("useInstalledPlugins", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  const lastOnEvent = () => streamMock.mock.calls.at(-1)![0].onEvent;

  it("lists the channel's plugins and follows the stream of its channel", async () => {
    fetchMock.mockResolvedValue(plugins);
    const { result } = renderHook(() => useInstalledPlugins("ch1"));
    expect(result.current).toBeNull();
    await waitFor(() => expect(result.current).toBe(plugins));
    expect(fetchMock).toHaveBeenCalledWith("ch1");
    expect(streamMock).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: "ch1" }));
  });

  it("lists them again on a plugins.changed event and on the window event, not on other events", async () => {
    fetchMock.mockResolvedValue(plugins);
    renderHook(() => useInstalledPlugins(null));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    act(() => lastOnEvent()({ type: "agent.status", data: {} } as WSEvent));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    act(() => lastOnEvent()({ type: "plugins.changed", data: {} } as WSEvent));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    act(() => {
      window.dispatchEvent(new Event(PLUGINS_CHANGED));
    });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(fetchMock).toHaveBeenLastCalledWith(null);
  });

  it("lists none when listing fails", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    const { result } = renderHook(() => useInstalledPlugins("ch1"));
    await waitFor(() => expect(result.current).toEqual([]));
    expect(console.warn).toHaveBeenCalledWith("[loop] listing plugins:", expect.any(Error));
  });

  it("drops what a channel it left answers", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    let ok!: (p: PluginInfo[]) => void;
    let fail!: (e: Error) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (ok = r))).mockReturnValueOnce(new Promise((_, r) => (fail = r)));
    fetchMock.mockResolvedValue([cogtest]);
    const { result, rerender } = renderHook(({ id }) => useInstalledPlugins(id), { initialProps: { id: "a" } });
    rerender({ id: "b" });
    rerender({ id: "c" });
    await waitFor(() => expect(result.current).toEqual([cogtest]));
    await act(async () => {
      ok(plugins);
      fail(new Error("late"));
    });
    expect(result.current).toEqual([cogtest]);
  });
});
