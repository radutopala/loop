import { describe, expect, it } from "vitest";

import type { WorktreeInfo } from "../../api/git";
import { filterWorktrees } from "./WorktreeRows";

const login: WorktreeInfo = { path: "/repo/.worktrees/login-fix", branch: "feat/Login", thread_id: "t1" };
const detached: WorktreeInfo = { path: "/repo/.worktrees/scratch", branch: "" };
const all = [login, detached];

describe("filterWorktrees", () => {
  it.each([
    ["", all],
    ["   ", all],
    ["login", [login]],
    [" LOGIN ", [login]],
    ["fix", [login]],
    ["scratch", [detached]],
    ["worktrees", []],
    ["nope", []],
  ])("search %j", (search, want) => {
    expect(filterWorktrees(all, search)).toEqual(want);
  });

  it("returns a copy, not the input", () => {
    expect(filterWorktrees(all, "")).not.toBe(all);
  });
});
