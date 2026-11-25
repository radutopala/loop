import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createBranch, deleteBranch, fetchBranches, fetchCommitDiff, fetchCommits, fetchDiff, fetchPR, switchBranch } from "./git";

const API = "http://localhost:8222";

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

function respond(body: string | null, init: ResponseInit = {}) {
  fetchMock.mockImplementation(() => Promise.resolve(new Response(body, init)));
}

function json(value: unknown) {
  respond(JSON.stringify(value));
}

function sent(i = 0): { url: string; method?: string; body?: unknown } {
  const [url, init] = fetchMock.mock.calls[i]!;
  const raw = (init as RequestInit | undefined)?.body;
  return { url: String(url), method: (init as RequestInit | undefined)?.method, body: typeof raw === "string" ? JSON.parse(raw) : raw };
}

const fail = () => respond("", { status: 500, statusText: "Bad" });

describe("fetchBranches", () => {
  it.each([
    [undefined, `${API}/api/channels/ch1/branches`],
    [0, `${API}/api/channels/ch1/branches`],
    [2, `${API}/api/channels/ch1/branches?root=2`],
  ])("GETs root %s", async (root, url) => {
    const info = { branches: ["main"], current: "main", worktrees: [] };
    json(info);
    await expect(fetchBranches("ch1", root)).resolves.toEqual(info);
    expect(sent().url).toBe(url);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(fetchBranches("ch1")).rejects.toThrow("Failed to fetch branches: Bad");
  });
});

describe("branch writes", () => {
  it.each([
    ["switchBranch", () => switchBranch("ch1", "dev"), `${API}/api/channels/ch1/branches/switch`, "POST", { branch: "dev" }],
    ["deleteBranch", () => deleteBranch("ch1", "dev"), `${API}/api/channels/ch1/branches`, "DELETE", { branch: "dev" }],
  ] as const)("%s sends the branch and throws the daemon's message", async (_name, run, url, method, body) => {
    respond(null, { status: 204 });
    await run();
    expect(sent()).toEqual({ url, method, body });
    respond("uncommitted changes", { status: 409 });
    await expect(run()).rejects.toThrow("uncommitted changes");
  });

  it.each([
    [undefined, { name: "feat" }],
    ["main", { name: "feat", from: "main" }],
  ])("createBranch from %s", async (from, body) => {
    respond(null, { status: 204 });
    await createBranch("ch1", "feat", from);
    expect(sent()).toEqual({ url: `${API}/api/channels/ch1/branches/create`, method: "POST", body });
  });

  it("createBranch throws with the status text", async () => {
    fail();
    await expect(createBranch("ch1", "feat")).rejects.toThrow("Failed to create branch: Bad");
  });
});

describe("fetchCommits", () => {
  it.each<[[string?, number?, number?, number?], string]>([
    [[], `${API}/api/channels/ch1/commits`],
    [["dev", 20, 40, 3], `${API}/api/channels/ch1/commits?branch=dev&limit=20&skip=40&root=3`],
    [["", 0, 0, 0], `${API}/api/channels/ch1/commits`],
  ])("GETs %o", async (args, url) => {
    json({ commits: [{ hash: "a", short: "a", subject: "s", author: "x", date: "d" }] });
    const got = await fetchCommits("ch1", ...args);
    expect(got).toEqual([{ hash: "a", short: "a", subject: "s", author: "x", date: "d" }]);
    expect(sent().url).toBe(url);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(fetchCommits("ch1")).rejects.toThrow("Failed to fetch commits: Bad");
  });
});

describe("fetchPR", () => {
  it.each([
    [false, `${API}/api/channels/ch1/pr`],
    [true, `${API}/api/channels/ch1/pr?fresh=1`],
  ])("GETs fresh=%s", async (fresh, url) => {
    json({ present: false });
    await expect(fetchPR("ch1", fresh)).resolves.toEqual({ present: false });
    expect(sent().url).toBe(url);
  });

  it("defaults to a cached lookup and throws with the status text", async () => {
    fail();
    await expect(fetchPR("ch1")).rejects.toThrow("Failed to fetch PR info: Bad");
    expect(sent().url).toBe(`${API}/api/channels/ch1/pr`);
  });
});

describe("fetchDiff", () => {
  it.each<[[string?, string?, number?], string]>([
    [[], `${API}/api/channels/ch1/diff`],
    [["feat", undefined], `${API}/api/channels/ch1/diff`],
    [["feat", "main"], `${API}/api/channels/ch1/diff?source=feat&target=main`],
    [["feat", "main", 2], `${API}/api/channels/ch1/diff?source=feat&target=main&root=2`],
    [[undefined, undefined, 1], `${API}/api/channels/ch1/diff?root=1`],
    [[undefined, undefined, 0], `${API}/api/channels/ch1/diff`],
  ])("GETs %o", async (args, url) => {
    json({ files: [], diff: "", total_additions: 0, total_deletions: 0 });
    await expect(fetchDiff("ch1", ...args)).resolves.toMatchObject({ files: [] });
    expect(sent().url).toBe(url);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(fetchDiff("ch1")).rejects.toThrow("Failed to fetch diff: Bad");
  });
});

describe("fetchCommitDiff", () => {
  it.each([
    [undefined, `${API}/api/channels/ch1/diff?commit=abc`],
    [0, `${API}/api/channels/ch1/diff?commit=abc`],
    [2, `${API}/api/channels/ch1/diff?commit=abc&root=2`],
  ])("GETs root %s", async (root, url) => {
    json({ files: [], diff: "x", total_additions: 1, total_deletions: 0 });
    await expect(fetchCommitDiff("ch1", "abc", root)).resolves.toMatchObject({ diff: "x" });
    expect(sent().url).toBe(url);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(fetchCommitDiff("ch1", "abc")).rejects.toThrow("Failed to fetch commit diff: Bad");
  });
});
