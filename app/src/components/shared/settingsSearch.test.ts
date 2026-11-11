import { describe, expect, it } from "vitest";
import type { ConfigSchema } from "../../api/configApi";
import { searchSettings } from "./settingsSearch";

const schema: ConfigSchema = {
  type: "object",
  properties: {
    dictation: {
      type: "object",
      title: "Dictation",
      "x-section": "Dictation",
      properties: {
        model: { type: "string", title: "Model" },
        idle_stop_sec: { type: "integer", title: "Idle stop (s)" },
      },
    },
    mcp_servers: {
      type: "object",
      title: "MCP servers",
      "x-section": "MCP",
      additionalProperties: {
        type: "object",
        properties: {
          command: { type: "string" },
          env: { type: "object", additionalProperties: { type: "object", properties: { secret_ref: { type: "string" } } } },
        },
      },
    },
    mounts: {
      type: "array",
      "x-section": "Workspace",
      items: { type: "object", properties: { host_path: { type: "string" }, options: { type: "object", properties: { read_only: { type: "boolean" } } } } },
    },
    log_level: { type: "string", title: "Log level", "x-section": "General", "x-global-only": true },
  },
};

const sections = ["Daemon", "Dictation", "MCP", "Workspace", "General", "JSON"];

// paths flattens a result to section → matched key paths.
function paths(query: string, isGlobal = true): Record<string, string[]> | null {
  const r = searchSettings(schema, isGlobal, sections, query);
  return r && Object.fromEntries([...r].map(([name, hits]) => [name, hits.map((h) => h.path)]));
}

describe("searchSettings", () => {
  it("returns null for a blank query", () => {
    expect(searchSettings(schema, true, sections, "  ")).toBeNull();
  });

  it.each([
    { name: "parent key finds every key inside it", query: "dictation", want: { Dictation: ["dictation.model", "dictation.idle_stop_sec"] } },
    { name: "dotted path", query: "dictation.model", want: { Dictation: ["dictation.model"] } },
    { name: "words across the path, any separator", query: "dictation idle", want: { Dictation: ["dictation.idle_stop_sec"] } },
    { name: "title", query: "log level", want: { General: ["log_level"] } },
    { name: "case-insensitive", query: "HOST_PATH", want: { Workspace: ["mounts[].host_path"] } },
    { name: "keys in list entries, nested", query: "read_only", want: { Workspace: ["mounts[].options.read_only"] } },
    { name: "keys in map entries, nested", query: "secret", want: { MCP: ["mcp_servers.*.env.*.secret_ref"] } },
    { name: "section name only", query: "daemon", want: { Daemon: [] } },
    { name: "no match", query: "nothing here", want: {} },
  ])("$name", ({ query, want }) => {
    expect(paths(query)).toEqual(want);
  });

  it("jumps to the list holding a matched entry key", () => {
    expect(searchSettings(schema, true, sections, "host_path")?.get("Workspace")).toEqual([{ field: "mounts", path: "mounts[].host_path" }]);
  });

  it("follows the config scope", () => {
    expect(paths("log level", false)).toEqual({});
  });

  it("works before the schema loads", () => {
    expect(searchSettings(null, true, sections, "json")).toEqual(new Map([["JSON", []]]));
  });
});
