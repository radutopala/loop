import { describe, expect, it } from "vitest";
import type { NotificationInfo } from "../api/notifications";
import type { PluginInfo } from "../api/plugins";
import type { Channel } from "../types";
import { cardChannels, channelLabel, newestId, notificationHeading, notificationSource, unreadLabel } from "./notifications";

const note = (over: Partial<NotificationInfo>): NotificationInfo => ({ id: 1, kind: "run", read: false, created_at: "", ...over });
const plugin = (id: string, name?: string): PluginInfo => ({ id, name, version: "1", capabilities: [], status: "ok" });
const channel = (id: string, name: string, parent_id = ""): Channel => ({ id, name, parent_id }) as Channel;

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

describe("channelLabel", () => {
  const channels = [channel("c1", "loop"), channel("t1", "updates", "c1"), channel("t2", "orphan", "gone")];
  it.each([
    ["none", undefined, ""],
    ["a channel", "c1", "loop"],
    ["a thread", "t1", "loop › updates"],
    ["a thread whose parent is gone", "t2", "orphan"],
    ["a deleted channel", "x", ""],
  ])("%s", (_, id, want) => {
    expect(channelLabel(id, channels)).toBe(want);
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

describe("cardChannels", () => {
  it("lists the cards' channels once each", () => {
    const list = [
      note({ kind: "card", channel_id: "a" }),
      note({ kind: "run", channel_id: "b" }),
      note({ kind: "card", channel_id: "c" }),
      note({ kind: "card", channel_id: "a" }),
      note({ kind: "card" }),
    ];
    expect(cardChannels(list)).toEqual(["a", "c"]);
  });
});
