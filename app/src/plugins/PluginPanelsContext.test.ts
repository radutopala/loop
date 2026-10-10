import { describe, expect, it } from "vitest";
import type { PluginInfo } from "../api/plugins";
import type { PaneNode } from "../types/panels";
import { findPluginPanel, pluginPaneKey, pluginPaneOptions, shownPluginPanes } from "./PluginPanelsContext";

const quiz = { id: "quiz", label: "Quiz", entry: "quiz/index.html", singleton: true };
const cogtest: PluginInfo = {
  id: "cogtest",
  name: "Cognitive tests",
  version: "0.1.0",
  capabilities: ["ui.panel"],
  status: "installed",
  panels: [quiz, { id: "log", label: "", entry: "log.html" }],
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

it("pluginPaneKey", () => {
  expect(pluginPaneKey({ id: "a", panel: "b" })).toBe("a/b");
});
