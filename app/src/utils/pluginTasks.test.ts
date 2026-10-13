import { describe, expect, it } from "vitest";
import type { PluginInfo } from "../api/plugins";
import { pluginRunOptions, pluginTaskEntry, pluginTaskOptions, scheduledActions, scheduleState } from "./pluginTasks";

describe("scheduledActions", () => {
  const p = (more: Partial<PluginInfo>): PluginInfo => ({ id: "echo", version: "1", capabilities: ["schedule"], status: "installed", actions: ["daily", "ping", "pong", "gone"], ...more });
  it("lists the actions with a default schedule or a config asking for one", () => {
    expect(scheduledActions(p({ default_schedules: { daily: { type: "cron", schedule: "0 9 * * *" } }, schedules: { ping: {} }, project_schedules: { pong: {}, ghost: {} } }))).toEqual([
      "daily",
      "ping",
      "pong",
    ]);
    expect(scheduledActions(p({ global_schedules: { gone: {} } }))).toEqual(["gone"]);
  });
  it("lists none without schedule approved", () => {
    expect(scheduledActions(p({ capabilities: [], schedules: { ping: {} } }))).toEqual([]);
    expect(scheduledActions(p({ actions: undefined, schedules: { ping: {} } }))).toEqual([]);
  });
});

describe("scheduleState", () => {
  const def = { type: "cron", schedule: "0 9 * * *" };
  const p = (more: Partial<PluginInfo>): PluginInfo => ({
    id: "echo",
    version: "1",
    capabilities: ["schedule"],
    status: "installed",
    actions: ["daily"],
    default_schedules: { daily: def },
    ...more,
  });

  it("starts a new entry off, on the default schedule", () => {
    for (const ch of [null, "ch-1"]) {
      expect(scheduleState(p({}), "daily", ch)).toEqual({ own: undefined, on: false, base: { enabled: false }, fallback: def, pending: false });
    }
  });

  it("reads the global entry without a channel", () => {
    const g = { schedule: "1h", type: "interval" };
    expect(scheduleState(p({ global_schedules: { daily: g } }), "daily")).toEqual({ own: g, on: true, base: g, fallback: def, pending: false });
    expect(scheduleState(p({ global_schedules: { daily: { enabled: false } } }), "daily").on).toBe(false);
  });

  it("follows the global entry in a project without its own", () => {
    const g = { schedule: "0 6 * * *" };
    expect(scheduleState(p({ global_schedules: { daily: g }, schedules: { daily: g } }), "daily", "ch-1")).toEqual({ own: undefined, on: true, base: g, fallback: g, pending: false });
  });

  it("reads the project's own entry, pending until trusted", () => {
    const own = { enabled: false };
    const applied = p({ global_schedules: { daily: {} }, project_schedules: { daily: own }, schedules: { daily: own } });
    expect(scheduleState(applied, "daily", "ch-1")).toEqual({ own, on: false, base: own, fallback: def, pending: false });
    const untrusted = p({ global_schedules: { daily: {} }, project_schedules: { daily: own }, schedules: { daily: {} } });
    expect(scheduleState(untrusted, "daily", "ch-1")).toMatchObject({ on: true, pending: true });
  });
});

describe("pluginTaskOptions", () => {
  const def = { type: "cron", schedule: "0 9 * * *" };
  const plugin = (more: Partial<PluginInfo>): PluginInfo => ({
    id: "echo",
    name: "Echo",
    version: "1",
    capabilities: ["schedule"],
    status: "installed",
    actions: ["daily", "now"],
    default_schedules: { daily: def },
    enabled: true,
    ...more,
  });

  it("lists nothing without plugins", () => {
    expect(pluginTaskOptions(null, "ch-1")).toEqual([]);
    expect(pluginTaskOptions({ off: true, plugins: [plugin({})] }, "ch-1")).toEqual([]);
    expect(pluginTaskOptions({ off: false, plugins: [plugin({ enabled: false })] }, "ch-1")).toEqual([]);
  });

  it("starts each action on its default schedule", () => {
    const [o, ...rest] = pluginTaskOptions({ off: false, plugins: [plugin({})] }, "ch-1");
    expect(rest).toEqual([]);
    expect(o).toMatchObject({ key: "echo/daily", label: "Echo: daily", action: "daily", type: "cron", schedule: "0 9 * * *", scheduled: false });
  });

  it("starts on what the project asks for", () => {
    const own = { type: "interval", schedule: "2h" };
    const [o] = pluginTaskOptions({ off: false, plugins: [plugin({ name: undefined, project_schedules: { daily: own }, schedules: { daily: own } })] }, "ch-1");
    expect(o).toMatchObject({ label: "echo: daily", type: "interval", schedule: "2h", scheduled: true });
  });

  it("falls back to the type's default schedule", () => {
    const [o] = pluginTaskOptions({ off: false, plugins: [plugin({ default_schedules: undefined, schedules: { now: { enabled: false } }, global_schedules: { now: { enabled: false } } })] }, "ch-1");
    expect(o).toMatchObject({ key: "echo/now", type: "cron", schedule: "*/30 * * * *", scheduled: false });
  });
});

describe("pluginTaskEntry", () => {
  const p: PluginInfo = {
    id: "echo",
    version: "1",
    capabilities: ["schedule"],
    status: "installed",
    actions: ["daily"],
    default_schedules: { daily: { type: "cron", schedule: "0 9 * * *" } },
    enabled: true,
  };
  const [o] = pluginTaskOptions({ off: false, plugins: [p] }, "ch-1");

  it.each([
    ["the default schedule", "cron", " 0 9 * * * ", { enabled: true, type: undefined, schedule: undefined }],
    ["another cron", "cron", "0 7 * * *", { enabled: true, type: undefined, schedule: "0 7 * * *" }],
    ["an interval", "interval", "1h", { enabled: true, type: "interval", schedule: "1h" }],
  ] as const)("asks for %s", (_, type, schedule, want) => {
    expect(pluginTaskEntry(o!, type, schedule)).toEqual(want);
  });

  it("keeps the rest of the entry it starts from", () => {
    const own = { enabled: false, schedule: "0 6 * * *" };
    const [o] = pluginTaskOptions({ off: false, plugins: [{ ...p, project_schedules: { daily: own }, schedules: { daily: own } }] }, "ch-1");
    expect(pluginTaskEntry(o!, "cron", "0 6 * * *")).toEqual({ enabled: true, type: undefined, schedule: "0 6 * * *" });
  });
});

describe("pluginTaskEntry, turned off", () => {
  it("keeps the schedule", () => {
    const p: PluginInfo = {
      id: "echo",
      version: "1",
      capabilities: ["schedule"],
      status: "installed",
      actions: ["daily"],
      default_schedules: { daily: { type: "cron", schedule: "0 9 * * *" } },
      enabled: true,
    };
    const [o] = pluginTaskOptions({ off: false, plugins: [p] }, "ch-1");
    expect(pluginTaskEntry(o!, "interval", "3h", false)).toEqual({ enabled: false, type: "interval", schedule: "3h" });
  });
});

describe("pluginRunOptions", () => {
  it("is nothing without a worktree", () => {
    expect(pluginRunOptions(false, "main", true, 60)).toBeUndefined();
  });

  it("holds the worktree's options", () => {
    expect(pluginRunOptions(true, " main ", true, 60)).toEqual({ worktree: true, origin_branch: "main", update_before_run: true, auto_delete_sec: 60 });
  });
});
