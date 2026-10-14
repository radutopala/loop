import { describe, expect, it } from "vitest";
import type { Monitor } from "../../api/monitors";
import { applyMonitorEvent } from "./monitorEvents";

const mon = (id: string): Monitor => ({ id, channel_id: "c1", description: id, command: "sleep 1", started_at: "2026-01-02T03:04:05Z", timeout_sec: 60, interrupt: false });
const event = (type: string, data: unknown, channel_id = "c1") => ({ type, channel_id, data, timestamp: 0 });

describe("applyMonitorEvent", () => {
  it("adds a started monitor once", () => {
    const list = applyMonitorEvent([mon("a")], event("monitor.started", mon("b")), "c1");
    expect(list.map((m) => m.id)).toEqual(["a", "b"]);
    expect(applyMonitorEvent(list, event("monitor.started", mon("b")), "c1")).toBe(list);
  });

  it("drops a finished monitor", () => {
    const list = [mon("a"), mon("b")];
    expect(applyMonitorEvent(list, event("monitor.finished", { id: "a", channel_id: "c1", status: "cancelled" }), "c1").map((m) => m.id)).toEqual(["b"]);
    expect(applyMonitorEvent(list, event("monitor.finished", { id: "z", channel_id: "c1", status: "cancelled" }), "c1")).toBe(list);
  });

  it("ignores other channels and event types", () => {
    const list = [mon("a")];
    expect(applyMonitorEvent(list, event("monitor.started", mon("b"), "c2"), "c1")).toBe(list);
    expect(applyMonitorEvent(list, event("agent.status", {}), "c1")).toBe(list);
  });
});
