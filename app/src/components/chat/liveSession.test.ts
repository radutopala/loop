import { describe, expect, it } from "vitest";
import type { LiveStatus } from "../../api/live";
import { applyLiveEvent, endedReason, footerLabel, liveBackground, liveIcon, liveIconTitle, liveSummary, shouldWarm } from "./liveSession";

const event = (type: string, data: unknown, channel_id = "c1") => ({ type, channel_id, data, timestamp: 0 });
const running: LiveStatus = { running: true, enabled: true, channel_id: "c1", busy: false };

describe("applyLiveEvent", () => {
  it("replaces the status with a live.status event, keeping enabled", () => {
    const ended = { running: false, channel_id: "c1", busy: false, reason: "idle" };
    expect(applyLiveEvent(running, event("live.status", ended), "c1")).toEqual({ ...ended, enabled: true });
    expect(applyLiveEvent(null, event("live.status", ended), "c1")).toEqual(ended);
  });

  it("follows the background tasks of a running session", () => {
    const waiting = applyLiveEvent(running, event("agent.activity", { activity: "background_tasks", description: "1 background task: build" }), "c1");
    expect(waiting?.background).toBe("1 background task: build");
    expect(applyLiveEvent(waiting, event("agent.activity", { activity: "background_tasks", description: "" }), "c1")?.background).toBeUndefined();
  });

  it.each([
    { name: "another channel's event", status: running, ev: event("live.status", { running: false }, "c2") },
    { name: "another activity", status: running, ev: event("agent.activity", { activity: "model", model: "m" }) },
    { name: "an activity with no session running", status: { ...running, running: false }, ev: event("agent.activity", { activity: "background_tasks", description: "x" }) },
    { name: "another event type", status: running, ev: event("agent.status", {}) },
  ])("leaves the status as it is for $name", ({ status, ev }) => {
    expect(applyLiveEvent(status, ev, "c1")).toBe(status);
  });
});

describe("endedReason", () => {
  it.each([
    ["idle", "stopped after being idle"],
    ["config changed", "restarting with the new config"],
    ["scheduled run", "stopped for a scheduled task"],
    ["stopped", "stopped"],
    ["shutdown", "stopped when Loop shut down"],
    ["stdin closed", "its input closed"],
    ["interrupt timed out", "it didn't stop when interrupted"],
    ["exited", "it exited"],
    ["session changed", "the chat moved to another session"],
    ["one-shot run", "stopped for a one-shot run"],
    ["closed", "its pane closed"],
    ["deleted", "its channel was deleted"],
    ["exited: exit status 1", "it exited: exit status 1"],
    ["restart failed: no image", "it couldn't restart: no image"],
    ["something new", "something new"],
  ])("%s", (reason, want) => {
    expect(endedReason(reason)).toBe(want);
  });
});

describe("liveSummary", () => {
  it.each<{ name: string; status: LiveStatus | null; want: string | null }>([
    { name: "no status yet", status: null, want: null },
    { name: "no session", status: { running: false, busy: false }, want: null },
    { name: "a session with no background tasks", status: running, want: null },
    { name: "background tasks", status: { ...running, background: "2 background tasks" }, want: null },
    { name: "an ended session", status: { running: false, busy: false, reason: "scheduled run" }, want: "Session ended: stopped for a scheduled task" },
    { name: "a session ended idle", status: { running: false, busy: false, reason: "idle" }, want: null },
    { name: "a session stopped", status: { running: false, busy: false, reason: "stopped" }, want: null },
  ])("$name", ({ status, want }) => {
    expect(liveSummary(status)).toBe(want);
  });
});

describe("liveBackground", () => {
  it.each<{ name: string; status: LiveStatus | null; want: string | null }>([
    { name: "no status yet", status: null, want: null },
    { name: "a session with no background tasks", status: running, want: null },
    { name: "background tasks", status: { ...running, background: "2 background tasks" }, want: "2 background tasks" },
    { name: "an ended session", status: { running: false, busy: false, background: "2 background tasks" }, want: null },
  ])("$name", ({ status, want }) => {
    expect(liveBackground(status)).toBe(want);
  });
});

describe("liveIcon", () => {
  it.each<{ name: string; status: LiveStatus | null; busy: boolean; want: string }>([
    { name: "no status yet", status: null, busy: false, want: "off" },
    { name: "no session", status: { running: false, busy: false }, busy: false, want: "off" },
    { name: "an idle session", status: running, busy: false, want: "idle" },
    { name: "a stale busy snapshot", status: { ...running, busy: true }, busy: false, want: "idle" },
    { name: "a turn running", status: running, busy: true, want: "working" },
    { name: "a one-shot turn running", status: null, busy: true, want: "working" },
  ])("$name", ({ status, busy, want }) => {
    expect(liveIcon(status, busy)).toBe(want);
  });

  it.each([
    ["working", "Working"],
    ["idle", "Live session idle"],
    ["off", "No live session running"],
  ] as const)("titles %s", (icon, want) => {
    expect(liveIconTitle(icon)).toBe(want);
  });
});

describe("footerLabel", () => {
  it.each([
    [true, "Running interactively in an isolated Docker container"],
    [false, "Running non-interactively in an isolated Docker container"],
    [undefined, "Running interactively in an isolated Docker container"],
  ])("live enabled %s", (enabled, want) => {
    expect(footerLabel(enabled)).toBe(want);
  });
});

describe("shouldWarm", () => {
  const idle: LiveStatus = { running: false, enabled: true, busy: false };
  it.each([
    ["status not loaded", null, false, false],
    ["live turned off", { ...idle, enabled: false }, false, false],
    ["already running", { ...idle, running: true }, false, false],
    ["already asked", idle, true, false],
    ["ended event, enabled unknown", { running: false, busy: false }, false, true],
    ["not running", idle, false, true],
  ])("%s", (_, status, asked, want) => {
    expect(shouldWarm(status as LiveStatus | null, asked as boolean)).toBe(want);
  });
});
