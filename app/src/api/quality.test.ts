import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch, unreadable } from "../test/hooks-c-api-fetch";
import {
  fetchQualityC4,
  fetchQualityClones,
  fetchQualityComplexity,
  fetchQualityCycles,
  fetchQualityEvolution,
  fetchQualityRules,
  fetchQualitySnapshot,
  simulateQualityWhatif,
  triggerQualityScan,
} from "./quality";

const Q = `${API}/api/channels/c%201/quality`;

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("triggerQualityScan", () => {
  it("posts a scan", async () => {
    fetchMock.mockResolvedValue(json({ status: "started" }));
    await expect(triggerQualityScan("c 1")).resolves.toEqual({ status: "started" });
    expect(sent(fetchMock)).toMatchObject({ url: `${Q}/scan`, method: "POST" });
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(triggerQualityScan("c1")).rejects.toThrow("Failed to trigger scan: boom");
  });
});

describe("fetchQualitySnapshot", () => {
  it("returns the snapshot", async () => {
    fetchMock.mockResolvedValue(json({ signal: 0.8 }));
    await expect(fetchQualitySnapshot("c 1")).resolves.toEqual({ signal: 0.8 });
    expect(sent(fetchMock).url).toBe(`${Q}/snapshot`);
  });

  it("returns null before the first scan", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    await expect(fetchQualitySnapshot("c1")).resolves.toBeNull();
  });

  it("throws on another error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchQualitySnapshot("c1")).rejects.toThrow("Failed to fetch snapshot: boom");
  });
});

describe("analyses", () => {
  it.each([
    ["cycles", fetchQualityCycles],
    ["rules", fetchQualityRules],
    ["evolution", fetchQualityEvolution],
    ["c4", fetchQualityC4],
  ] as const)("fetches %s", async (path, run) => {
    fetchMock.mockResolvedValue(json({ ok: path }));
    await expect(run("c 1")).resolves.toEqual({ ok: path });
    expect(sent(fetchMock).url).toBe(`${Q}/${path}`);
  });

  it("returns null evolution when there is no history", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    await expect(fetchQualityEvolution("c1")).resolves.toBeNull();
  });

  it.each([
    ["complexity", fetchQualityComplexity],
    ["clones", fetchQualityClones],
  ] as const)("pages %s", async (path, run) => {
    fetchMock.mockImplementation(async () => json({ returned: 0 }));
    await expect(run("c 1", { limit: 5, offset: 10 })).resolves.toEqual({ returned: 0 });
    await run("c 1", { offset: 0 });
    await run("c 1");
    expect(sent(fetchMock, 0).url).toBe(`${Q}/${path}?limit=5&offset=10`);
    expect(sent(fetchMock, 1).url).toBe(`${Q}/${path}?offset=0`);
    expect(sent(fetchMock, 2).url).toBe(`${Q}/${path}`);
  });

  it("simulates mutations", async () => {
    fetchMock.mockResolvedValue(json({ delta_signal: 0.1 }));
    const mutations = [{ op: "split" as const, path: "a.go", parts: 2 }];
    await expect(simulateQualityWhatif("c 1", mutations)).resolves.toEqual({ delta_signal: 0.1 });
    expect(sent(fetchMock)).toMatchObject({ url: `${Q}/whatif`, method: "POST", body: { mutations } });
    expect(sent(fetchMock).headers.get("content-type")).toBe("application/json");
  });

  it.each([
    ["cycles", () => fetchQualityCycles("c1")],
    ["rules", () => fetchQualityRules("c1")],
    ["evolution", () => fetchQualityEvolution("c1")],
    ["c4", () => fetchQualityC4("c1")],
    ["complexity", () => fetchQualityComplexity("c1")],
    ["clones", () => fetchQualityClones("c1")],
    ["whatif", () => simulateQualityWhatif("c1", [])],
  ])("%s throws the labelled server text, else the status text", async (label, run) => {
    fetchMock.mockResolvedValueOnce(fail(500, "boom", " no scan yet \n"));
    await expect(run()).rejects.toThrow(new RegExp(`^${label}: no scan yet$`));
    fetchMock.mockResolvedValueOnce(fail(500, "boom"));
    await expect(run()).rejects.toThrow(`${label}: boom`);
  });

  it("falls back to the status text when the body can't be read", async () => {
    fetchMock.mockResolvedValue(unreadable(500, "boom"));
    await expect(simulateQualityWhatif("c1", [])).rejects.toThrow("whatif: boom");
  });
});
