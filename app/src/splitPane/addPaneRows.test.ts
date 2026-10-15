import { describe, expect, it } from "vitest";
import type { PluginInfo } from "../api/plugins";
import { canvasAddRows } from "../canvas/canvasAddRows";
import { type PanelType, paneLabel } from "../types/panels";
import { addPaneRows } from "./addPaneRows";

const plugins: PluginInfo[] = [
  { id: "cogtest", name: "Cognitive tests", version: "1", capabilities: [], status: "installed", panels: [{ id: "quiz", label: "Quiz", entry: "q.html", singleton: true }] },
];
const quiz = { id: "cogtest", panel: "quiz", label: "Quiz" };

describe("addPaneRows", () => {
  it("offers docker-agent once per open mode and the plugin panels last", () => {
    const rows = addPaneRows(new Set(), { plugins, shown: new Set() });
    expect(rows.filter((r) => r.panel === "docker-agent").map((r) => r.meta)).toEqual([{ openMode: "resume" }, { openMode: "fork" }, { openMode: "fresh" }]);
    expect(rows.at(-1)).toEqual({ key: "plugin-cogtest/quiz", panel: "plugin", label: "Quiz", meta: { plugin: quiz }, disabled: false, description: "Cognitive tests plugin" });
    expect(rows.some((r) => r.panel === "plugin" && r.key === "plugin")).toBe(false);
  });

  it("offers chat-side as a fork or a fresh session, and Chat once", () => {
    const rows = addPaneRows(new Set(["chat-side"]), { plugins: [], shown: new Set() });
    expect(rows.filter((r) => r.panel === "chat-side").map((r) => [r.label, r.meta, r.disabled])).toEqual([
      ["Chat (Resume with fork)", { openMode: "fork" }, false],
      ["Chat (Fresh session)", { openMode: "fresh" }, false],
    ]);
    expect(rows.filter((r) => r.panel === "chat").map((r) => r.meta)).toEqual([undefined]);
  });

  it.each<{ name: string; used: PanelType[]; panel: PanelType; disabled: boolean }>([
    { name: "a used singleton", used: ["chat"], panel: "chat", disabled: true },
    { name: "an unused singleton", used: [], panel: "chat", disabled: false },
    { name: "excluded by its group", used: ["docker-browser"], panel: "host-browser", disabled: true },
    { name: "a repeatable panel", used: ["host-shell"], panel: "host-shell", disabled: false },
  ])("$name", ({ used, panel, disabled }) => {
    expect(addPaneRows(new Set(used), { plugins: [], shown: new Set() }).find((r) => r.panel === panel)?.disabled).toBe(disabled);
  });

  it("drops hidden panels and disables a shown singleton plugin panel", () => {
    const rows = addPaneRows(new Set(), { plugins, shown: new Set(["cogtest/quiz"]) }, ["chat"]);
    expect(rows.some((r) => r.panel === "chat")).toBe(false);
    expect(rows.at(-1)?.disabled).toBe(true);
  });
});

describe("canvasAddRows", () => {
  it("lists the built-ins, then the plugin panels", () => {
    const rows = canvasAddRows(new Set<PanelType>(["chat"]), { plugins, shown: new Set() }, ["editor"]);
    expect(rows[0]).toEqual({ key: "chat", panel: "chat", label: "Chat", disabled: true });
    expect(rows.some((r) => r.panel === "editor")).toBe(false);
    expect(rows.filter((r) => r.panel === "docker-agent")).toHaveLength(1);
    expect(rows.at(-1)).toEqual({ key: "plugin-cogtest/quiz", panel: "plugin", label: "Quiz", meta: { plugin: quiz }, disabled: false });
  });

  it("lists chat-side once per mode it takes", () => {
    const rows = canvasAddRows(new Set<PanelType>(["chat-side"]), { plugins: null, shown: new Set() });
    expect(rows.filter((r) => r.panel === "chat-side")).toEqual([
      { key: "chat-side-fork", panel: "chat-side", label: "Chat (Resume with fork)", meta: { openMode: "fork" }, disabled: false },
      { key: "chat-side-fresh", panel: "chat-side", label: "Chat (Fresh session)", meta: { openMode: "fresh" }, disabled: false },
    ]);
  });

  it("enables everything with no used set", () => {
    expect(canvasAddRows(undefined, { plugins: null, shown: new Set() }).every((r) => !r.disabled)).toBe(true);
  });
});

describe("paneLabel", () => {
  it.each<{ openMode?: "fork" | "fresh"; want: string }>([{ openMode: "fork", want: "Chat · fork" }, { openMode: "fresh", want: "Chat · fresh" }, { want: "Chat · fork" }])(
    "heads a chat-side pane opened as $openMode with $want",
    ({ openMode, want }) => {
      expect(paneLabel("chat-side", undefined, openMode)).toBe(want);
    },
  );
});
