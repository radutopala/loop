import { describe, expect, it } from "vitest";
import type { NotificationInfo } from "../api/notifications";
import type { PluginInfo } from "../api/plugins";
import { newestId, notificationHeading, notificationSource, unreadLabel, unreadTitle } from "./notifications";

const note = (over: Partial<NotificationInfo>): NotificationInfo => ({ id: 1, kind: "run", read: false, created_at: "", ...over });
const plugin = (id: string, name?: string): PluginInfo => ({ id, name, version: "1", capabilities: [], status: "ok" });

describe("notificationSource", () => {
  it.each([
    ["Loop's own", note({}), [], "Loop"],
    ["a plugin by name", note({ plugin_id: "quiz" }), [plugin("quiz", "Quiz")], "Quiz"],
    ["a plugin without a name", note({ plugin_id: "quiz" }), [plugin("quiz")], "quiz"],
    ["a plugin not listed", note({ plugin_id: "quiz" }), null, "quiz"],
  ])("%s", (_, n, plugins, want) => {
    expect(notificationSource(n, plugins)).toBe(want);
  });
});

describe("notificationHeading", () => {
  it.each([
    ["the title", note({ title: "Run failed" }), "Run failed"],
    ["a card's id without a title", note({ kind: "card", card: "score" }), "score"],
    ["a card's title", note({ kind: "card", card: "score", title: "Quiz" }), "Quiz"],
    ["nothing", note({}), ""],
  ])("%s", (_, n, want) => {
    expect(notificationHeading(n)).toBe(want);
  });
});

describe("newestId", () => {
  it("is the largest id, 0 for none", () => {
    expect(newestId([])).toBe(0);
    expect(newestId([note({ id: 3 }), note({ id: 9 }), note({ id: 5 })])).toBe(9);
  });
});

describe("unreadLabel", () => {
  it.each([
    [0, ""],
    [-1, ""],
    [7, "7"],
    [99, "99"],
    [100, "99+"],
  ])("%i", (n, want) => {
    expect(unreadLabel(n)).toBe(want);
  });
});

describe("unreadTitle", () => {
  it.each([
    [1, "1 unread notification"],
    [3, "3 unread notifications"],
  ])("%i", (n, want) => {
    expect(unreadTitle(n)).toBe(want);
  });
});
