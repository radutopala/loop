import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createWorktreeThread } from "./channels";
import { fetchOpenPRs } from "./git";

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

describe("fetchOpenPRs", () => {
  it("returns the repo's open PRs", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ prs: [{ number: 12, head_ref: "feat/x", base_ref: "main" }] })));
    expect(await fetchOpenPRs("ch1")).toEqual([{ number: 12, head_ref: "feat/x", base_ref: "main" }]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/ch1/prs");
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("gh: not logged in\n", { status: 502 }));
    await expect(fetchOpenPRs("ch1")).rejects.toThrow(/^gh: not logged in$/);
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(fetchOpenPRs("ch1")).rejects.toThrow("Failed to list the open PRs: Internal Server Error");
  });
});

describe("createWorktreeThread", () => {
  it("sends the PR to check out", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ thread_id: "t1", worktree_path: "/repo/.worktrees/pr-12" })));
    expect(await createWorktreeThread("ch1", "main", undefined, 12)).toEqual({ threadId: "t1", worktreePath: "/repo/.worktrees/pr-12" });
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ channel_id: "ch1", branch: "main", pr: 12 });
  });

  it("sends a plain branch with its name", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ thread_id: "t1", worktree_path: "/w" })));
    await createWorktreeThread("ch1", "main", "wt");
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ channel_id: "ch1", branch: "main", name: "wt" });
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("no such PR", { status: 500, statusText: "Internal Server Error" }));
    await expect(createWorktreeThread("ch1", "main", undefined, 99)).rejects.toThrow("Failed to create worktree: no such PR");
  });
});
