import { describe, expect, it } from "vitest";
import type { WSEvent } from "../types";
import { startedRunThread } from "./PluginAgent";

function event(type: string, data: unknown): WSEvent {
  return { type, data } as WSEvent;
}

describe("startedRunThread", () => {
  it.each([
    { name: "the plugin's run", ev: event("plugin.run_started", { plugin_id: "cogtest", run_id: "r1", thread_id: "plugin:cogtest-abc123" }), want: "plugin:cogtest-abc123" },
    { name: "another plugin's run", ev: event("plugin.run_started", { plugin_id: "echo", thread_id: "plugin:echo-abc123" }), want: null },
    { name: "a run without a thread", ev: event("plugin.run_started", { plugin_id: "cogtest" }), want: null },
    { name: "an event without data", ev: event("plugin.run_started", undefined), want: null },
    { name: "another event", ev: event("agent.status", { plugin_id: "cogtest", thread_id: "plugin:cogtest-abc123" }), want: null },
  ])("answers $name", ({ ev, want }) => {
    expect(startedRunThread(ev, "cogtest")).toBe(want);
  });
});
