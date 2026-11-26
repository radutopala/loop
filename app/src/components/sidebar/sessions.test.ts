import { describe, expect, it } from "vitest";
import type { Channel } from "../../types";
import { activeSessions, isSearching, isTaskThread, matchesSearch, parseSearch, RECENT_WINDOW_MS, recentSessions, relativeTime, sessionContext, sessionName } from "./sessions";

function ch(id: string, over: Partial<Channel> = {}): Channel {
  return {
    id,
    name: id,
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
    ...over,
  };
}

describe("isTaskThread", () => {
  it.each([
    { desc: "a task's thread", over: { task_id: 3 }, want: true },
    { desc: "a renamed task's thread", over: { task_id: 3, name: "renamed" }, want: true },
    { desc: "a thread named like a task's", over: { name: "task #3 (`1m`) check" }, want: false },
    { desc: "a thread with task_id 0", over: { task_id: 0 }, want: false },
  ])("$desc is $want", ({ over, want }) => {
    expect(isTaskThread(ch("t", { name: "t", parent_id: "p", ...over }))).toBe(want);
  });
});

describe("sessionName", () => {
  it.each([
    { name: "plain", channel: ch("a", { name: "updates" }), want: "updates" },
    { name: "task thread loses its marker", channel: ch("a", { name: "🧵 task #12 (`0 9 * * *`)" }), want: "task #12 (`0 9 * * *`)" },
    { name: "ephemeral task", channel: ch("a", { name: "[ephemeral] ⏱ task #3" }), want: "task #3" },
    { name: "ephemeral non-task keeps its name", channel: ch("a", { name: "[ephemeral] scratch" }), want: "[ephemeral] scratch" },
    { name: "unnamed channel uses its dir", channel: ch("a", { name: "", dir_path: "/home/u/loop" }), want: "loop" },
    { name: "nothing else falls back to the id", channel: ch("a", { name: "" }), want: "a" },
  ])("$name", ({ channel, want }) => {
    expect(sessionName(channel)).toBe(want);
  });
});

describe("matchesSearch", () => {
  const thread = ch("t", {
    name: "Login fix",
    description: "Retry the token refresh",
    ticket_url: "https://tickets.example.com/browse/ABC-42",
    branch: "fix/login-retry",
    pr_kind: "pr",
    pr_number: 401,
    pr_title: "fix(auth): retry the refresh",
  });

  it.each([
    ["", true],
    ["login", true],
    ["token refresh", true],
    ["abc-42", true],
    ["login-retry", true],
    ["#401", true],
    ["401", true],
    ["fix(auth)", true],
    ["!401", false],
    ["nothing", false],
  ])("%j matches: %s", (query, want) => {
    expect(matchesSearch(thread, query)).toBe(want);
  });

  it("writes a merge request as !N", () => {
    const mr = ch("m", { pr_kind: "mr", pr_number: 7 });
    expect(matchesSearch(mr, "!7")).toBe(true);
    expect(matchesSearch(mr, "#7")).toBe(false);
  });

  it("leaves out a detached HEAD and fields that aren't set", () => {
    expect(matchesSearch(ch("d", { name: "x", branch: "HEAD" }), "head")).toBe(false);
    expect(matchesSearch(ch("n", { name: "x" }), "#")).toBe(false);
  });

  it("matches the name as the sidebar shows it", () => {
    expect(matchesSearch(ch("d", { name: "", dir_path: "/src/webapp" }), "webapp")).toBe(true);
  });

  it.each([
    ["tag:api", true],
    ["TAG:#Web", true],
    ["tag:api tag:web", true],
    ["tag:api tag:ops", false],
    ["tag:api login", true],
    ["login tag:api", true],
    ["tag:api nothing", false],
    ["tag:", true],
    ["tag:ap", false],
  ])("with tags, %j matches: %s", (query, want) => {
    expect(matchesSearch({ ...thread, tags: ["api", "web"] }, query)).toBe(want);
  });

  it("needs the tags on a session without any", () => {
    expect(matchesSearch(thread, "tag:api")).toBe(false);
  });

  it("matches the text in the context given", () => {
    expect(matchesSearch(thread, "Backend", "backend › web")).toBe(true);
    expect(matchesSearch(thread, "backend")).toBe(false);
  });
});

describe("parseSearch", () => {
  it.each([
    ["", { tags: [], text: "" }],
    ["  Login  Fix ", { tags: [], text: "login fix" }],
    ["tag:API tag:#web retry", { tags: ["api", "web"], text: "retry" }],
    ["tag: retry", { tags: [], text: "retry" }],
  ])("%j", (query, want) => {
    expect(parseSearch(query)).toEqual(want);
  });

  it("tells whether a search filters anything", () => {
    expect(isSearching("")).toBe(false);
    expect(isSearching("tag:")).toBe(false);
    expect(isSearching("tag:x")).toBe(true);
    expect(isSearching("x")).toBe(true);
  });
});

describe("sessionContext", () => {
  const byId = new Map([ch("loop"), ch("updates", { parent_id: "loop" }), ch("fork", { parent_id: "updates" })].map((c) => [c.id, c]));

  it.each([
    { name: "top-level channel has none", id: "loop", want: "" },
    { name: "thread shows its channel", id: "updates", want: "loop" },
    { name: "sub-thread shows the whole path", id: "fork", want: "loop › updates" },
  ])("$name", ({ id, want }) => {
    expect(sessionContext(byId.get(id)!, byId)).toBe(want);
  });

  it("stops on a parent cycle", () => {
    const cyc = new Map([ch("a", { parent_id: "b" }), ch("b", { parent_id: "a" })].map((c) => [c.id, c]));
    expect(sessionContext(cyc.get("a")!, cyc)).toBe("b");
  });
});

describe("activeSessions", () => {
  const channels = [ch("run1"), ch("idle"), ch("gate"), ch("run2"), ch("", { name: "" })];
  const running = new Set(["run1", "run2", "gate", ""]);
  const waiting = new Set(["gate"]);

  it("lists waiting sessions first, then running ones, skipping unlisted rows", () => {
    const got = activeSessions(
      channels,
      (id) => running.has(id),
      (id) => waiting.has(id),
    ).map((c) => c.id);
    expect(got).toEqual(["gate", "run1", "run2"]);
  });
});

describe("recentSessions", () => {
  const now = 1_000_000_000;
  const at: Record<string, number | undefined> = { old: now - RECENT_WINDOW_MS - 1, a: now - 5_000, b: now - 60_000, active: now, never: undefined };
  const channels = Object.keys(at).map((id) => ch(id));

  it("keeps the window's sessions, newest first", () => {
    const got = recentSessions(channels, (c) => at[c.id], now).map((c) => c.id);
    expect(got).toEqual(["active", "a", "b"]);
  });

  it("honours a custom window", () => {
    const got = recentSessions(channels, (c) => at[c.id], now, 10_000).map((c) => c.id);
    expect(got).toEqual(["active", "a"]);
  });
});

describe("relativeTime", () => {
  it.each([
    { ms: -5, want: "now" },
    { ms: 59_000, want: "now" },
    { ms: 60_000, want: "1m" },
    { ms: 59 * 60_000, want: "59m" },
    { ms: 3 * 3_600_000, want: "3h" },
    { ms: 30 * 3_600_000, want: "30h" },
    { ms: 47 * 3_600_000, want: "47h" },
    { ms: 48 * 3_600_000, want: "2d" },
    { ms: 80 * 3_600_000, want: "3d" },
  ])("$ms ms is $want", ({ ms, want }) => {
    expect(relativeTime(ms)).toBe(want);
  });
});
