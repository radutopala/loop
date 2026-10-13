import { describe, expect, it } from "vitest";
import type { PluginInfo } from "../../api/plugins";
import { imageRepo, scheduledActions } from "./PluginsSection";

describe("imageRepo", () => {
  it.each([
    [undefined, ""],
    ["echo", "echo"],
    ["echo:1", "echo"],
    ["ghcr.io/me/echo:1.2@sha256:ab", "ghcr.io/me/echo"],
    ["localhost:5000/echo", "localhost:5000/echo"],
    ["localhost:5000/echo:dev", "localhost:5000/echo"],
  ])("%s → %s", (image, want) => {
    expect(imageRepo(image)).toBe(want);
  });
});

describe("scheduledActions", () => {
  const p = (more: Partial<PluginInfo>): PluginInfo => ({ id: "echo", version: "1", capabilities: ["schedule"], status: "installed", actions: ["daily", "ping", "pong", "gone"], ...more });
  it("lists the actions with a default schedule or a config asking for one", () => {
    expect(scheduledActions(p({ default_schedules: { daily: { type: "cron", schedule: "0 9 * * *" } }, schedules: { ping: {} }, project_schedules: { pong: {}, ghost: {} } }))).toEqual([
      "daily",
      "ping",
      "pong",
    ]);
  });
  it("lists none without schedule approved", () => {
    expect(scheduledActions(p({ capabilities: [], schedules: { ping: {} } }))).toEqual([]);
    expect(scheduledActions(p({ actions: undefined, schedules: { ping: {} } }))).toEqual([]);
  });
});
