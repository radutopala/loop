import { describe, expect, it } from "vitest";
import type { Channel } from "../../types";
import { rowInfoLines } from "./rowInfo";

const base: Channel = {
  id: "ch",
  name: "ch",
  parent_id: "",
  dir_path: "",
  session_id: "",
  active: true,
  container_running: false,
  agent_running: false,
  branch: "",
  commit: "",
  worktree: false,
  locked: false,
  diff_additions: 0,
  diff_deletions: 0,
  review_enabled: false,
};

describe("rowInfoLines", () => {
  it.each<[string, Partial<Channel>, ReturnType<typeof rowInfoLines>]>([
    ["nothing for a channel without a dir or git", {}, []],
    ["the path", { dir_path: "/p" }, [{ key: "path", value: "/p" }]],
    [
      "the path before the branch",
      { dir_path: "/p", branch: "main" },
      [
        { key: "path", value: "/p" },
        { key: "branch", value: "main" },
      ],
    ],
    ["no root checkout", { dir_path: "/p/.worktrees/w", root_dir_path: "/p" }, [{ key: "path", value: "/p/.worktrees/w" }]],
    [
      "a branch, its commit and subject",
      { branch: "main", commit: "abc1234def", subject: "fix: a bug" },
      [
        { key: "branch", value: "main" },
        { key: "commit", value: "abc1234", detail: "fix: a bug" },
      ],
    ],
    [
      "a commit without a subject",
      { branch: "main", commit: "abc1234def" },
      [
        { key: "branch", value: "main" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    ["a branch without a commit", { branch: "main" }, [{ key: "branch", value: "main" }]],
    ["a worktree's branch and its base", { branch: "feat/x", worktree: true, base_branch: "main" }, [{ key: "branch", value: "feat/x", detail: "(from main)" }]],
    ["a worktree without a base", { branch: "feat/x", worktree: true }, [{ key: "branch", value: "feat/x" }]],
    ["no base on a plain channel", { branch: "main", base_branch: "dev" }, [{ key: "branch", value: "main" }]],
    [
      "a detached checkout",
      { commit: "abc1234def" },
      [
        { key: "branch", value: "detached" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    [
      "HEAD as the branch",
      { branch: "HEAD", commit: "abc1234def" },
      [
        { key: "branch", value: "detached" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    ["the distance from the base", { sync_base: "main", base_ahead: 3, base_behind: 1 }, [{ key: "sync", value: "↑3 ↓1 vs main" }]],
    ["only ahead of the base", { sync_base: "main", base_ahead: 2 }, [{ key: "sync", value: "↑2 vs main" }]],
    ["even with the base", { sync_base: "main" }, [{ key: "sync", value: "even with main" }]],
    ["only behind the upstream", { upstream: "origin/main", behind: 4 }, [{ key: "sync", value: "↓4 vs origin/main" }]],
    ["the base and the upstream", { sync_base: "main", base_ahead: 1, upstream: "origin/feat", ahead: 1 }, [{ key: "sync", value: "↑1 vs main · ↑1 vs origin/feat" }]],
    ["no distance without a base or upstream", { base_ahead: 3, ahead: 2 }, []],
    ["the model and effort", { model_override: "claude-opus-5-5", effort_override: "high" }, [{ key: "model", value: "claude-opus-5-5 · high" }]],
    ["only the effort", { effort_override: "low" }, [{ key: "model", value: "low" }]],
    ["a running agent", { agent_running: true, container_running: true }, [{ key: "status", value: "agent running" }]],
    ["a running container", { container_running: true }, [{ key: "status", value: "container running" }]],
    ["a locked channel", { locked: true }, [{ key: "status", value: "locked" }]],
    ["a running agent on a locked channel", { agent_running: true, locked: true }, [{ key: "status", value: "agent running · locked" }]],
  ])("shows %s", (_, overrides, want) => {
    expect(rowInfoLines({ ...base, ...overrides })).toEqual(want);
  });
});
