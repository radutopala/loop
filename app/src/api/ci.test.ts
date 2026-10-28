import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { cancelMerge, fixCI, getCI, getCILog, mergePR, rerunCI, setCIAutoFix } from "./ci";

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

describe("getCI", () => {
  it("returns the PR/MR's CI", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ present: true, ci: { channel_id: "ch1", kind: "mr", number: 57 } })));
    expect(await getCI("ch1")).toMatchObject({ kind: "mr", number: 57 });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/ch1/ci");
  });

  it("returns null without a PR/MR", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ present: false })));
    expect(await getCI("ch1")).toBeNull();
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("CI watching not configured", { status: 501 }));
    await expect(getCI("ch1")).rejects.toThrow("CI watching not configured");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(getCI("ch1")).rejects.toThrow("Failed to fetch CI: Internal Server Error");
  });
});

describe("CI actions", () => {
  it.each([
    { name: "fix", call: () => fixCI("ch1"), path: "/ci/fix", method: "POST", body: undefined },
    { name: "re-run", call: () => rerunCI("ch1"), path: "/ci/rerun", method: "POST", body: undefined },
    { name: "auto-fix off", call: () => setCIAutoFix("ch1", false), path: "/ci/auto-fix", method: "PUT", body: '{"auto_fix":false}' },
    { name: "auto-fix back to the config's", call: () => setCIAutoFix("ch1", null), path: "/ci/auto-fix", method: "PUT", body: '{"auto_fix":null}' },
    { name: "merge when ready", call: () => mergePR("ch1", true), path: "/pr/merge", method: "POST", body: '{"when_ready":true}' },
    { name: "merge now with a method", call: () => mergePR("ch1", false, "rebase"), path: "/pr/merge", method: "POST", body: '{"when_ready":false,"method":"rebase"}' },
    { name: "cancel the merge", call: () => cancelMerge("ch1"), path: "/pr/merge", method: "DELETE", body: undefined },
  ])("$name", async ({ call, path, method, body }) => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await call();
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe(`http://localhost:8222/api/channels/ch1${path}`);
    expect(init.method).toBe(method);
    expect(init.body).toBe(body);
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("no failure to fix", { status: 409 }));
    await expect(fixCI("ch1")).rejects.toThrow("no failure to fix");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 502, statusText: "Bad Gateway" }));
    await expect(cancelMerge("ch1")).rejects.toThrow("Failed to cancel the merge: Bad Gateway");
  });
});

describe("getCILog", () => {
  it("returns the log's tail", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ log: "FAIL lint" })));
    expect(await getCILog("ch1", "7 7")).toBe("FAIL lint");
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/ch1/ci/log?check=7%207");
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("no such failed check", { status: 404 }));
    await expect(getCILog("ch1", "9")).rejects.toThrow("no such failed check");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 502, statusText: "Bad Gateway" }));
    await expect(getCILog("ch1", "9")).rejects.toThrow("Failed to fetch the log: Bad Gateway");
  });
});
