import { describe, expect, it } from "vitest";
import type { LiveStatus } from "../../api/live";
import { applyLiveEvent, endedReason, footerLabel, liveBackground, liveIcon, liveIconTitle, liveSummary, shouldWarm, shownBackground, taskTypeLabel } from "./liveSession";

const event = (type: string, data: unknown, channel_id = "c1") => ({ type, channel_id, data, timestamp: 0 });
const running: LiveStatus = { running: true, enabled: true, channel_id: "c1", busy: false };
const build = [{ id: "b1", type: "local_bash", description: "build", started_at: "2026-10-16T09:00:00Z" }];

describe("applyLiveEvent", () => {
  it("replaces the status with a live.status event, keeping enabled", () => {
    const ended = { running: false, channel_id: "c1", busy: false, reason: "idle" };
    expect(applyLiveEvent(running, event("live.status", ended), "c1")).toEqual({ ...ended, enabled: true });
    expect(applyLiveEvent(null, event("live.status", ended), "c1")).toEqual(ended);
  });

  it("follows the background tasks of a running session", () => {
    const waiting = applyLiveEvent(running, event("agent.activity", { activity: "background_tasks", description: "1 background task: build", tasks: build }), "c1");
    expect(waiting?.background).toBe("1 background task: build");
    expect(waiting?.background_tasks).toEqual(build);
    const done = applyLiveEvent(waiting, event("agent.activity", { activity: "background_tasks", description: "", tasks: [] }), "c1");
    expect(done?.background).toBeUndefined();
    expect(done?.background_tasks).toBeUndefined();
    expect(applyLiveEvent(running, event("agent.activity", { activity: "background_tasks", description: "x" }), "c1")?.background_tasks).toBeUndefined();
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
  it.each<{ name: string; status: LiveStatus | null; want: unknown }>([
    { name: "no status yet", status: null, want: null },
    { name: "a session with no background tasks", status: running, want: null },
    { name: "an empty task list", status: { ...running, background_tasks: [] }, want: null },
    { name: "background tasks", status: { ...running, background_tasks: build }, want: build },
    { name: "an ended session", status: { running: false, busy: false, background_tasks: build }, want: null },
  ])("$name", ({ status, want }) => {
    expect(liveBackground(status)).toEqual(want);
  });
});

describe("shownBackground", () => {
  const ended: LiveStatus = { running: false, busy: false };
  it.each<{ name: string; status: LiveStatus | null; run: typeof build | null; isRunning: boolean; want: unknown }>([
    { name: "a live session's tasks, stoppable", status: { ...running, background_tasks: build }, run: null, isRunning: false, want: { tasks: build, stoppable: true } },
    { name: "a live session without tasks", status: running, run: build, isRunning: true, want: null },
    { name: "a one-shot run's tasks", status: ended, run: build, isRunning: true, want: { tasks: build, stoppable: false } },
    { name: "with no status yet", status: null, run: build, isRunning: true, want: { tasks: build, stoppable: false } },
    { name: "a run that ended", status: ended, run: build, isRunning: false, want: null },
    { name: "a run without tasks", status: ended, run: [], isRunning: true, want: null },
    { name: "nothing", status: null, run: null, isRunning: false, want: null },
  ])("$name", ({ status, run, isRunning, want }) => {
    expect(shownBackground(status, run, isRunning)).toEqual(want);
  });
});

describe("taskTypeLabel", () => {
  it.each([
    ["local_bash", "Shell"],
    ["local_agent", "Agent"],
    ["remote_agent", "Remote agent"],
    ["local_workflow", "Workflow"],
    ["monitor", "Monitor"],
    ["monitor_mcp", "Monitor"],
    ["monitor_ws", "Monitor"],
    ["in_process_teammate", "Teammate"],
    ["dream", "Background"],
    [undefined, "Background"],
  ])("%s", (type, want) => {
    expect(taskTypeLabel(type)).toBe(want);
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
