import { describe, expect, it } from "vitest";

import type { PRInfo } from "../../api/git";
import { matchesPR, prAction, prRef, prWorktree, showsPRs } from "./OpenPRs";

const pr: PRInfo = { number: 12, url: "https://example.com/pull/12", base_ref: "main", head_ref: "feat/login", state: "OPEN", title: "Fix the login" };
const mr: PRInfo = { ...pr, number: 7, kind: "mr" };

describe("prRef", () => {
  it("writes a PR with # and an MR with !", () => {
    expect(prRef(pr)).toBe("#12");
    expect(prRef(mr)).toBe("!7");
  });
});

describe("matchesPR", () => {
  it.each([
    ["", true],
    ["  ", true],
    ["#12", true],
    ["12", true],
    ["LOGIN", true],
    ["feat/", true],
    ["fix the", true],
    ["!12", false],
    ["other", false],
  ])("%j → %s", (search, want) => {
    expect(matchesPR(pr, search)).toBe(want);
  });

  it("matches a PR without a title by its branch", () => {
    expect(matchesPR({ ...pr, title: undefined }, "login")).toBe(true);
    expect(matchesPR({ ...pr, title: undefined }, "fix")).toBe(false);
  });
});

describe("prWorktree and prAction", () => {
  const worktrees = [
    { path: "/r/.worktrees/a", branch: "feat/other" },
    { path: "/r/.worktrees/b", branch: "feat/login" },
  ];

  it("finds the worktree on the PR's branch", () => {
    expect(prWorktree(pr, worktrees)).toBe(worktrees[1]);
    expect(prWorktree(pr, undefined)).toBeUndefined();
    expect(prWorktree({ ...pr, head_ref: "none" }, worktrees)).toBeUndefined();
  });

  it("goes to a worktree's thread, imports one without, or makes one", () => {
    expect(prAction(pr, { path: "/w", branch: "feat/login", thread_id: "t1" })).toEqual({ label: "go", title: "Go to the worktree thread on feat/login" });
    expect(prAction(pr, { path: "/w", branch: "feat/login" })).toEqual({ label: "imp", title: "Import the worktree on feat/login as a thread" });
    expect(prAction(mr, undefined)).toEqual({ label: "+wt", title: "Check !7 out on feat/login in a new worktree thread" });
  });
});

describe("showsPRs", () => {
  it("shows while loading, on an error, or with some open", () => {
    expect(showsPRs(null, null)).toBe(true);
    expect(showsPRs([], "gh: not logged in")).toBe(true);
    expect(showsPRs([pr], null)).toBe(true);
    expect(showsPRs([], null)).toBe(false);
  });
});
