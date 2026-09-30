import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchProjectTrust, ProjectTrustChangedError, trustProjectConfig } from "./configApi";

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
    const status = { trusted: false, current: '{"mounts": []}', approved: "", hash: "h1" };
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
