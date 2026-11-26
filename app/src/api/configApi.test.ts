import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ConfigChangedError,
  configRevisionSourceLabel,
  fetchBashShortcuts,
  fetchConfigHistory,
  fetchConfigHistoryFiles,
  fetchConfigRevision,
  fetchConfigSchema,
  fetchGlobalConfig,
  fetchProjectConfig,
  fetchProjectTrust,
  fetchShortcuts,
  GLOBAL_CONFIG_SAVED_EVENT,
  ProjectTrustChangedError,
  restoreConfigRevision,
  saveGlobalConfig,
  saveProjectConfig,
  setGlobalConfigValue,
  trustProjectConfig,
  withConfigValue,
} from "./configApi";

let fetchMock: ReturnType<typeof vi.fn>;
let dispatched: string[];

beforeEach(() => {
  dispatched = [];
  vi.stubGlobal("window", { location: { hash: "" }, dispatchEvent: (e: Event) => dispatched.push(e.type) });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchProjectConfig", () => {
  it("returns the channel's project config", async () => {
    const cfg = { path: "/p/.loop/config.json", content: { mounts: [] }, raw: '{"mounts": []}' };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(cfg)));
    await expect(fetchProjectConfig("ch 1")).resolves.toEqual(cfg);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/project?channel_id=ch+1");
  });

  it("throws with the server's reason", async () => {
    fetchMock.mockResolvedValue(new Response("project config leads outside the project: x\n", { status: 400, statusText: "Bad Request" }));
    await expect(fetchProjectConfig("ch-1")).rejects.toThrow("project config leads outside the project: x");
  });

  it("falls back to the status text on an empty body", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchProjectConfig("ch-1")).rejects.toThrow("Failed to fetch project config: boom");
  });
});

describe("fetchProjectTrust", () => {
  it("returns the channel's trust status", async () => {
    const status = { trusted: false, current: '{"mounts": []}', approved: "", diff: "--- /dev/null\n+++ .loop/config.json (now)\n", hash: "h1" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(status)));
    await expect(fetchProjectTrust("ch 1")).resolves.toEqual(status);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/project/trust?channel_id=ch+1");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchProjectTrust("ch-1")).rejects.toThrow("Failed to fetch project trust: boom");
  });
});

describe("trustProjectConfig", () => {
  it("posts the reviewed hash", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await trustProjectConfig("ch-1", "h1");
    const [url, init] = fetchMock.mock.calls[0]! as [string, RequestInit];
    expect(url).toBe("http://localhost:8222/api/config/project/trust?channel_id=ch-1");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ hash: "h1" });
  });

  it("reports a config that changed since it was reviewed", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 409 }));
    await expect(trustProjectConfig("ch-1", "h1")).rejects.toBeInstanceOf(ProjectTrustChangedError);
  });

  it("throws on other errors", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(trustProjectConfig("ch-1", "h1")).rejects.toThrow("Failed to trust project config: boom");
  });
});

describe("fetchConfigHistory", () => {
  const history = { path: "/p/.loop/config.json", revisions: [{ id: 2, source: "settings", created_at: "2026-10-09T10:00:00Z", added: 1, removed: 0 }] };

  it("fetches the global config's history", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(history)));
    await expect(fetchConfigHistory()).resolves.toEqual(history);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/history");
  });

  it("fetches a project config's history", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(history)));
    await fetchConfigHistory("ch 1");
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/project/history?channel_id=ch+1");
  });

  it("fetches another file's history", async () => {
    fetchMock.mockImplementation(async () => new Response(JSON.stringify(history)));
    await fetchConfigHistory(undefined, "templates/a.md");
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/history?file=templates%2Fa.md");
    await fetchConfigHistory("ch-1", "shortcuts/b.md");
    expect(fetchMock.mock.calls[1]![0]).toBe("http://localhost:8222/api/config/project/history?channel_id=ch-1&file=shortcuts%2Fb.md");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchConfigHistory()).rejects.toThrow("Failed to fetch config history: boom");
  });
});

describe("fetchConfigHistoryFiles", () => {
  const files = { root: "/p/.loop", files: [{ file: "config.json", revisions: 2, updated_at: "2026-10-09T10:00:00Z" }] };

  it("fetches the global .loop dir's files", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(files)));
    await expect(fetchConfigHistoryFiles()).resolves.toEqual(files);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/history/files");
  });

  it("fetches a project's files", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(files)));
    await fetchConfigHistoryFiles("ch 1");
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/project/history/files?channel_id=ch+1");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchConfigHistoryFiles()).rejects.toThrow("Failed to fetch config history files: boom");
  });
});

describe("fetchConfigRevision", () => {
  it("fetches the revision", async () => {
    const rev = { id: 2, path: "/p", content: "{}", hash: "h", source: "settings", created_at: "2026-10-09T10:00:00Z", diff: "" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(rev)));
    await expect(fetchConfigRevision(2)).resolves.toEqual(rev);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/history/2");
  });

  it("diffs against another revision", async () => {
    fetchMock.mockResolvedValue(new Response("{}"));
    await fetchConfigRevision(2, 7);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/history/2?against=7");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(fetchConfigRevision(2)).rejects.toThrow("Failed to fetch config revision: Not Found");
  });
});

describe("restoreConfigRevision", () => {
  it("posts the restore", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await restoreConfigRevision(2);
    const [url, init] = fetchMock.mock.calls[0]! as [string, RequestInit];
    expect(url).toBe("http://localhost:8222/api/config/history/2/restore");
    expect(init.method).toBe("POST");
    expect(dispatched).toEqual([GLOBAL_CONFIG_SAVED_EVENT]);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(restoreConfigRevision(2)).rejects.toThrow("Failed to restore config revision: boom");
    expect(dispatched).toEqual([]);
  });
});

describe("configRevisionSourceLabel", () => {
  it.each([
    ["settings", "Settings"],
    ["learn", "Learn"],
    ["shortcuts", "Shortcuts"],
    ["workflows", "Workflows"],
    ["builtins", "Built-ins"],
    ["external", "Edited outside Loop"],
    ["initial", "First seen"],
    ["restore:12", "Restored #12"],
    ["other", "other"],
  ])("labels %s", (source, label) => {
    expect(configRevisionSourceLabel(source)).toBe(label);
  });
});

// The response to a save: the saved file's hash.
function saved(hash = "h-saved"): Response {
  return new Response(JSON.stringify({ hash }));
}

describe("setGlobalConfigValue", () => {
  it("sets one key in the global config and keeps its comments", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ path: "", content: null, raw: '{\n  // mine\n  "desktop": {"theme": "light"}\n}', hash: "h1" })));
    fetchMock.mockResolvedValueOnce(saved());
    await setGlobalConfigValue(["desktop", "hidden_pills"], ["ci", "comment"]);
    const [url, init] = fetchMock.mock.calls[1] ?? [];
    expect(url).toMatch(/\/api\/config$/);
    expect(init.method).toBe("PUT");
    const { content, base_hash } = JSON.parse(init.body) as { content: string; base_hash: string };
    expect(base_hash).toBe("h1");
    expect(content).toContain("// mine");
    expect(content).toContain('"theme": "light"');
    expect(content).toMatch(/"hidden_pills": \[\s*"ci",\s*"comment"\s*\]/);
    expect(dispatched).toEqual([GLOBAL_CONFIG_SAVED_EVENT]);
  });
});

describe("fetchConfigSchema", () => {
  it("returns the schema", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ sections: [] })));
    await expect(fetchConfigSchema()).resolves.toEqual({ sections: [] });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config/schema");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchConfigSchema()).rejects.toThrow("Failed to fetch config schema: boom");
  });
});

describe("global config", () => {
  it("fetches it", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ raw: "{}", content: {} })));
    await expect(fetchGlobalConfig()).resolves.toEqual({ raw: "{}", content: {} });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/config");
  });

  it("throws when fetching fails", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchGlobalConfig()).rejects.toThrow("Failed to fetch global config: boom");
  });

  it("saves it from the file it was made from, returns the new hash and tells the window", async () => {
    fetchMock.mockResolvedValue(saved("h2"));
    await expect(saveGlobalConfig("{}", "h1")).resolves.toBe("h2");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/config");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(init.body)).toEqual({ content: "{}", base_hash: "h1" });
    expect(dispatched).toEqual([GLOBAL_CONFIG_SAVED_EVENT]);
  });

  it("saves it unchecked without a base hash", async () => {
    fetchMock.mockResolvedValue(saved());
    await saveGlobalConfig("{}");
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ content: "{}" });
  });

  it("refuses a save over a file that changed, without telling the window", async () => {
    fetchMock.mockResolvedValue(new Response("changed\n", { status: 409 }));
    await expect(saveGlobalConfig("{}", "h1")).rejects.toBeInstanceOf(ConfigChangedError);
    expect(dispatched).toEqual([]);
  });

  it("saves it without a window to tell", async () => {
    fetchMock.mockImplementation(async () => {
      vi.stubGlobal("window", undefined);
      return saved("h");
    });
    await expect(saveGlobalConfig("{}")).resolves.toBe("h");
    expect(dispatched).toEqual([]);
  });

  it("throws when saving fails, without telling the window", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(saveGlobalConfig("{}")).rejects.toThrow("Failed to save global config: boom");
    expect(dispatched).toEqual([]);
  });

  it("sets a value in a config with no raw text", async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ content: null })));
    fetchMock.mockResolvedValueOnce(saved());
    await setGlobalConfigValue(["a"], 1);
    expect(JSON.parse(JSON.parse(fetchMock.mock.calls[1]![1].body).content)).toEqual({ a: 1 });
  });
});

describe("saveProjectConfig", () => {
  it("puts the content for the channel", async () => {
    fetchMock.mockResolvedValue(saved("h2"));
    await expect(saveProjectConfig("c 1", "{}", "h1")).resolves.toBe("h2");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/config/project?channel_id=c+1");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(init.body)).toEqual({ content: "{}", base_hash: "h1" });
  });

  it("refuses a save over a file that changed", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 409 }));
    await expect(saveProjectConfig("c1", "{}", "h1")).rejects.toThrow("changed since it was loaded");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(saveProjectConfig("c1", "{}")).rejects.toThrow("Failed to save project config: boom");
  });
});

describe.each([
  ["fetchShortcuts", fetchShortcuts, "shortcuts"],
  ["fetchBashShortcuts", fetchBashShortcuts, "bash-shortcuts"],
] as const)("%s", (_name, run, path) => {
  it("lists them, with and without a channel", async () => {
    fetchMock.mockImplementation(async () => new Response(JSON.stringify([{ name: "s" }])));
    await expect(run("c 1")).resolves.toEqual([{ name: "s" }]);
    await run();
    expect(fetchMock.mock.calls[0]![0]).toBe(`http://localhost:8222/api/${path}?channel_id=c%201`);
    expect(fetchMock.mock.calls[1]![0]).toBe(`http://localhost:8222/api/${path}`);
  });

  it("returns none on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500 }));
    await expect(run("c1")).resolves.toEqual([]);
  });
});

describe("withConfigValue", () => {
  it("starts an empty file as an object", () => {
    expect(JSON.parse(withConfigValue("  ", ["a"], 1))).toEqual({ a: 1 });
  });

  it("refuses a file that doesn't parse", () => {
    expect(() => withConfigValue("{", ["a"], 1)).toThrow("the config file doesn't parse; fix it first");
  });

  it("keeps the file's indent width", () => {
    expect(withConfigValue('{\n    "a": 1\n}\n', ["b"], 2)).toBe('{\n    "a": 1,\n    "b": 2\n}\n');
  });

  it("keeps tab indents and CRLF line ends", () => {
    expect(withConfigValue('{\r\n\t"a": 1\r\n}\r\n', ["b"], 2)).toBe('{\r\n\t"a": 1,\r\n\t"b": 2\r\n}\r\n');
  });

  it("removes a key when the value is undefined", () => {
    expect(JSON.parse(withConfigValue('{"a": 1, "b": 2}', ["a"], undefined))).toEqual({ b: 2 });
  });
});
