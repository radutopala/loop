import { describe, expect, it } from "vitest";
import type { PRInfo } from "../../api/git";
import type { PRStatus } from "../../api/pr";
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
      "the ticket before the path",
      { dir_path: "/p", ticket_url: "https://example.atlassian.net/browse/PROJ-1" },
      [
        { key: "ticket", value: "https://example.atlassian.net/browse/PROJ-1" },
        { key: "path", value: "/p" },
      ],
    ],
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

describe("rowInfoLines with a PR", () => {
  const pr: PRInfo = { number: 12, url: "https://github.com/o/r/pull/12", base_ref: "main", head_ref: "feat", state: "OPEN", title: "Fix the login" };
  it.each<[string, PRInfo | null, ReturnType<typeof rowInfoLines>]>([
    [
      "after the branch, before the commit",
      pr,
      [
        { key: "branch", value: "feat" },
        { key: "pr", value: "#12", detail: "Fix the login (open)" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    [
      "its state alone without a title",
      { ...pr, title: undefined, is_draft: true },
      [
        { key: "branch", value: "feat" },
        { key: "pr", value: "#12", detail: "(draft)" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    [
      "a GitLab MR as !N",
      { ...pr, kind: "mr", state: "opened" },
      [
        { key: "branch", value: "feat" },
        { key: "pr", value: "!12", detail: "Fix the login (open)" },
        { key: "commit", value: "abc1234" },
      ],
    ],
    [
      "nothing without one",
      null,
      [
        { key: "branch", value: "feat" },
        { key: "commit", value: "abc1234" },
      ],
    ],
  ])("shows %s", (_, p, want) => {
    expect(rowInfoLines({ ...base, branch: "feat", commit: "abc1234def" }, p)).toEqual(want);
  });
});

describe("rowInfoLines with lit PR/MR pills", () => {
  const status: PRStatus = {
    channel_id: "ch",
    kind: "pr",
    number: 12,
    head_sha: "abc",
    base_ref: "main",
    checks: { total: 4, passed: 2, failed: 2, pending: 0 },
    failed: [
      { id: "1", name: "lint" },
      { id: "2", name: "test" },
    ],
    merge_state: "blocked",
    auto_merge: false,
    pr_attention: "failed",
    auto_fix: true,
    auto_fix_default: true,
    attempts: 0,
    max_attempts: 3,
    reason: "not_agent_commit",
  };
  it.each<[string, Partial<Channel>, PRStatus | null, ReturnType<typeof rowInfoLines>]>([
    [
      "a failure and its reason",
      { pr_attention: "failed" },
      status,
      [{ key: "ci", value: "✗ 2 failing", detail: "(lint, test) The head commit isn't one this thread's agent made, so auto-fix leaves it to you." }],
    ],
    ["a failure before the status loads", { pr_attention: "failed" }, null, [{ key: "ci", value: "failed" }]],
    [
      "an agent fixing it",
      { pr_attention: "fixing" },
      { ...status, failed: [], reason: "fix_sent" },
      [{ key: "ci", value: "✗ 2 failing · agent fixing", detail: "Auto-fix sent the failure to the agent." }],
    ],
    [
      "a fix once the checks re-run",
      { pr_attention: "fixing" },
      { ...status, checks: { total: 4, passed: 2, failed: 0, pending: 2 }, failed: [], reason: undefined },
      [{ key: "ci", value: "agent fixing" }],
    ],
    ["a fix before the status loads", { pr_attention: "fixing" }, null, [{ key: "ci", value: "agent fixing" }]],
    [
      "a failed status with nothing failing",
      { pr_attention: "failed" },
      { ...status, checks: { total: 0, passed: 0, failed: 0, pending: 0 }, failed: undefined, reason: undefined },
      [{ key: "ci", value: "failed" }],
    ],
    [
      "a merge conflict with its base",
      { pr_attention: "conflict" },
      { ...status, merge_state: "conflict", pr_attention: "conflict" },
      [{ key: "conflict", value: "with main", detail: "yours to resolve" }],
    ],
    ["a merge conflict before the status loads", { pr_attention: "conflict" }, null, [{ key: "conflict", value: "with its base", detail: "yours to resolve" }]],
    ["one review thread", { unresolved_threads: 1 }, null, [{ key: "cmt", value: "1 unresolved review thread" }]],
    [
      "a conflict and review threads",
      { pr_attention: "conflict", unresolved_threads: 3 },
      null,
      [
        { key: "conflict", value: "with its base", detail: "yours to resolve" },
        { key: "cmt", value: "3 unresolved review threads" },
      ],
    ],
  ])("shows %s", (_, overrides, s, want) => {
    expect(rowInfoLines({ ...base, ...overrides }, null, s)).toEqual(want);
  });
});
