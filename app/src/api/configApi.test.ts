import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { configRevisionSourceLabel, fetchConfigHistory, fetchConfigRevision, fetchProjectTrust, ProjectTrustChangedError, restoreConfigRevision, trustProjectConfig } from "./configApi";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
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

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchConfigHistory()).rejects.toThrow("Failed to fetch config history: boom");
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
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(restoreConfigRevision(2)).rejects.toThrow("Failed to restore config revision: boom");
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
