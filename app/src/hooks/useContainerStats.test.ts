import { describe, expect, it } from "vitest";
import type { ContainerStatsEntry } from "../api/channels";
import { type ContainerStatsByType, fmtBytes, statsForPanel } from "./useContainerStats";

function entry(id: string): ContainerStatsEntry {
  return { container_id: id, type: id, cpu_percent: 1, mem_usage: 1, mem_limit: 2 };
}

const stats: ContainerStatsByType = {
  agent: entry("agent"),
  shell: entry("shell"),
  chrome: entry("chrome"),
};

describe("statsForPanel", () => {
  it("maps each panel to the container it actually runs in", () => {
    expect(statsForPanel("chat", stats)).toBe(stats.agent);
    expect(statsForPanel("docker-agent", stats)).toBe(stats.shell);
    expect(statsForPanel("docker-browser", stats)).toBe(stats.chrome);
  });

  it("shows nothing for panels with no container of their own", () => {
    // Host mode drives the user's own Chrome, so there is no sidecar to report.
    expect(statsForPanel("host-browser", stats)).toBeUndefined();
    expect(statsForPanel("editor", stats)).toBeUndefined();
  });

  it("tolerates stats that have not arrived yet", () => {
    expect(statsForPanel("docker-browser", undefined)).toBeUndefined();
    expect(statsForPanel("docker-browser", {})).toBeUndefined();
  });
});

describe("fmtBytes", () => {
  it("uses megabytes below a gigabyte and gigabytes above", () => {
    expect(fmtBytes(242638848)).toBe("231M");
    expect(fmtBytes(2 * 1024 * 1024 * 1024)).toBe("2.0G");
  });
});
