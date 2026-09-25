import { describe, expect, it } from "vitest";
import type { Channel } from "../../types";
import { activeSessions, RECENT_WINDOW_MS, recentSessions, relativeTime, sessionContext, sessionName } from "./sessions";

function ch(id: string, over: Partial<Channel> = {}): Channel {
  return {
    id,
    name: id,
    parent_id: "",
    dir_path: "",
    session_id: "",
    active: true,
    container_running: false,
    agent_running: false,
    branch: "",
    commit: "",
    worktree: false,
    locked: false,
    diff_additions: 0,
    diff_deletions: 0,
    review_enabled: false,
    ...over,
  };
}

describe("sessionName", () => {
  it.each([
    { name: "plain", channel: ch("a", { name: "updates" }), want: "updates" },
    { name: "task thread loses its marker", channel: ch("a", { name: "🧵 task #12 (`0 9 * * *`)" }), want: "task #12 (`0 9 * * *`)" },
    { name: "ephemeral task", channel: ch("a", { name: "[ephemeral] ⏱ task #3" }), want: "task #3" },
    { name: "ephemeral non-task keeps its name", channel: ch("a", { name: "[ephemeral] scratch" }), want: "[ephemeral] scratch" },
    { name: "unnamed channel uses its dir", channel: ch("a", { name: "", dir_path: "/home/u/loop" }), want: "loop" },
    { name: "nothing else falls back to the id", channel: ch("a", { name: "" }), want: "a" },
  ])("$name", ({ channel, want }) => {
    expect(sessionName(channel)).toBe(want);
  });
});

describe("sessionContext", () => {
  const byId = new Map([ch("loop"), ch("updates", { parent_id: "loop" }), ch("fork", { parent_id: "updates" })].map((c) => [c.id, c]));

  it.each([
    { name: "top-level channel has none", id: "loop", want: "" },
    { name: "thread shows its channel", id: "updates", want: "loop" },
    { name: "sub-thread shows the whole path", id: "fork", want: "loop › updates" },
  ])("$name", ({ id, want }) => {
    expect(sessionContext(byId.get(id)!, byId)).toBe(want);
  });

  it("stops on a parent cycle", () => {
    const cyc = new Map([ch("a", { parent_id: "b" }), ch("b", { parent_id: "a" })].map((c) => [c.id, c]));
    expect(sessionContext(cyc.get("a")!, cyc)).toBe("b");
  });
});

describe("activeSessions", () => {
  const channels = [ch("run1"), ch("idle"), ch("gate"), ch("run2"), ch("", { name: "" })];
  const running = new Set(["run1", "run2", "gate", ""]);
  const waiting = new Set(["gate"]);

  it("lists waiting sessions first, then running ones, skipping unlisted rows", () => {
    const got = activeSessions(
      channels,
      (id) => running.has(id),
      (id) => waiting.has(id),
    ).map((c) => c.id);
    expect(got).toEqual(["gate", "run1", "run2"]);
  });
});

describe("recentSessions", () => {
  const now = 1_000_000_000;
  const at: Record<string, number | undefined> = { old: now - RECENT_WINDOW_MS - 1, a: now - 5_000, b: now - 60_000, active: now, never: undefined };
  const channels = Object.keys(at).map((id) => ch(id));

  it("keeps the window's sessions, newest first, leaving out the excluded", () => {
    const got = recentSessions(channels, new Set(["active"]), (c) => at[c.id], now).map((c) => c.id);
    expect(got).toEqual(["a", "b"]);
  });

  it("honours a custom window", () => {
    const got = recentSessions(channels, new Set(), (c) => at[c.id], now, 10_000).map((c) => c.id);
    expect(got).toEqual(["active", "a"]);
  });
});

describe("relativeTime", () => {
  it.each([
    { ms: -5, want: "now" },
    { ms: 59_000, want: "now" },
    { ms: 60_000, want: "1m" },
    { ms: 59 * 60_000, want: "59m" },
    { ms: 3 * 3_600_000, want: "3h" },
    { ms: 50 * 3_600_000, want: "2d" },
  ])("$ms ms is $want", ({ ms, want }) => {
    expect(relativeTime(ms)).toBe(want);
  });
});
