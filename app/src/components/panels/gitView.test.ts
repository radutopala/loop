import { describe, expect, it, vi } from "vitest";
import type { CommitEntry, DiffResponse } from "../../api/loopApi";
import { COMMITS_PAGE, type GitView, type GitViewApi, type GitViewSink, gitViewKey, loadGitView } from "./gitView";

function patch(path: string): string {
  return `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1 +1 @@\n-old\n+new\n`;
}

function diff(extra: Partial<DiffResponse> = {}): DiffResponse {
  return { files: [], diff: "", total_additions: 0, total_deletions: 0, ...extra };
}

function commit(n: number): CommitEntry {
  return { hash: `h${n}`, short: `s${n}`, subject: `commit ${n}`, author: "a", date: "2026-10-11" };
}

function view(extra: Partial<GitView> = {}): GitView {
  return { channelId: "ch1", mode: "uncommitted", sourceBranch: "main", targetBranch: "feat", commitBranch: "feat", rootIndex: 0, ...extra };
}

function sink() {
  return {
    commitsStarted: vi.fn(),
    commits: vi.fn(),
    commitsDone: vi.fn(),
    diff: vi.fn(),
    diffDone: vi.fn(),
  } satisfies GitViewSink;
}

function api(extra: Partial<GitViewApi> = {}) {
  return {
    fetchCommits: vi.fn<GitViewApi["fetchCommits"]>().mockResolvedValue([]),
    fetchDiff: vi.fn<GitViewApi["fetchDiff"]>().mockResolvedValue(diff()),
    ...extra,
  };
}

/** A promise settled from outside, to hold a fetch in flight. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("gitViewKey", () => {
  it("differs for every part of the view", () => {
    const base = gitViewKey(view());
    const changes: Partial<GitView>[] = [{ channelId: "ch2" }, { mode: "branches" }, { sourceBranch: "dev" }, { targetBranch: "x" }, { commitBranch: "y" }, { rootIndex: 1 }];
    for (const c of changes) expect(gitViewKey(view(c))).not.toBe(base);
    expect(gitViewKey(view())).toBe(base);
  });

  it("doesn't run parts together", () => {
    expect(gitViewKey(view({ sourceBranch: "a", targetBranch: "bc" }))).not.toBe(gitViewKey(view({ sourceBranch: "ab", targetBranch: "c" })));
  });
});

describe("loadGitView", () => {
  it("loads the uncommitted diff, one file per status section", async () => {
    const d = diff({ diff: "x", staged_diff: patch("a.go"), unstaged_diff: patch("a.go"), conflict_diff: patch("c.go"), untracked_diff: patch("u.go") });
    const a = api({ fetchDiff: vi.fn<GitViewApi["fetchDiff"]>().mockResolvedValue(d) });
    const s = sink();
    await loadGitView(view({ rootIndex: 2 }), a, () => true, s);
    expect(a.fetchDiff).toHaveBeenCalledWith("ch1", undefined, undefined, 2);
    expect(s.diff).toHaveBeenCalledOnce();
    expect(s.diff).toHaveBeenCalledWith(d, [
      expect.objectContaining({ path: "a.go", status: "staged" }),
      expect.objectContaining({ path: "a.go", status: "unstaged" }),
      expect.objectContaining({ path: "c.go", status: "conflict" }),
      expect.objectContaining({ path: "u.go", status: "untracked" }),
    ]);
    expect(s.diffDone).toHaveBeenCalledOnce();
  });

  it("loads the branch diff from its patch", async () => {
    const a = api({ fetchDiff: vi.fn<GitViewApi["fetchDiff"]>().mockResolvedValue(diff({ diff: patch("b.go") })) });
    const s = sink();
    await loadGitView(view({ mode: "branches" }), a, () => true, s);
    expect(a.fetchDiff).toHaveBeenCalledWith("ch1", "main", "feat", 0);
    expect(s.diff).toHaveBeenCalledWith(expect.anything(), [expect.objectContaining({ path: "b.go" })]);
    expect(s.diffDone).toHaveBeenCalledOnce();
  });

  it.each([
    { name: "source", sourceBranch: "", targetBranch: "feat" },
    { name: "target", sourceBranch: "main", targetBranch: "" },
  ])("skips a branch diff without a $name branch", async ({ sourceBranch, targetBranch }) => {
    const a = api();
    const s = sink();
    await loadGitView(view({ mode: "branches", sourceBranch, targetBranch }), a, () => true, s);
    expect(a.fetchDiff).not.toHaveBeenCalled();
    expect(s.diffDone).not.toHaveBeenCalled();
  });

  it("ends a failed diff load without data", async () => {
    const s = sink();
    await loadGitView(view(), api({ fetchDiff: vi.fn<GitViewApi["fetchDiff"]>().mockRejectedValue(new Error("down")) }), () => true, s);
    expect(s.diff).not.toHaveBeenCalled();
    expect(s.diffDone).toHaveBeenCalledOnce();
  });

  it("drops a branch diff that lands after a switch to uncommitted", async () => {
    const branches = deferred<DiffResponse>();
    const a = api({
      fetchDiff: vi
        .fn<GitViewApi["fetchDiff"]>()
        .mockReturnValueOnce(branches.promise)
        .mockResolvedValueOnce(diff({ unstaged_diff: patch("mine.go") })),
    });
    const shown = { key: gitViewKey(view({ mode: "branches" })) };
    const s = sink();

    const first = view({ mode: "branches" });
    const late = loadGitView(first, a, () => shown.key === gitViewKey(first), s);
    // The user switches to uncommitted; its diff lands first.
    const second = view();
    shown.key = gitViewKey(second);
    await loadGitView(second, a, () => shown.key === gitViewKey(second), s);
    branches.resolve(diff({ diff: patch("branch.go") }));
    await late;

    expect(s.diff).toHaveBeenCalledOnce();
    expect(s.diff).toHaveBeenCalledWith(expect.anything(), [expect.objectContaining({ path: "mine.go", status: "unstaged" })]);
    expect(s.diffDone).toHaveBeenCalledOnce();
  });

  it("drops a failure that lands after the view changed", async () => {
    const pending = deferred<DiffResponse>();
    const s = sink();
    let current = true;
    const done = loadGitView(view(), api({ fetchDiff: () => pending.promise }), () => current, s);
    current = false;
    pending.reject(new Error("down"));
    await done;
    expect(s.diffDone).not.toHaveBeenCalled();
  });

  it("loads the first page of commits", async () => {
    const page = Array.from({ length: COMMITS_PAGE }, (_, i) => commit(i));
    const a = api({ fetchCommits: vi.fn<GitViewApi["fetchCommits"]>().mockResolvedValue(page) });
    const s = sink();
    await loadGitView(view({ mode: "commits", rootIndex: 1 }), a, () => true, s);
    expect(a.fetchCommits).toHaveBeenCalledWith("ch1", "feat", COMMITS_PAGE, 0, 1);
    expect(s.commitsStarted).toHaveBeenCalledOnce();
    expect(s.commits).toHaveBeenCalledWith(page, true);
    expect(s.commitsDone).toHaveBeenCalledOnce();
    expect(a.fetchDiff).not.toHaveBeenCalled();
  });

  it("has no more commits after a short page", async () => {
    const s = sink();
    await loadGitView(view({ mode: "commits" }), api({ fetchCommits: vi.fn<GitViewApi["fetchCommits"]>().mockResolvedValue([commit(1)]) }), () => true, s);
    expect(s.commits).toHaveBeenCalledWith([commit(1)], false);
  });

  it("empties the commits when loading them fails", async () => {
    const s = sink();
    await loadGitView(view({ mode: "commits" }), api({ fetchCommits: vi.fn<GitViewApi["fetchCommits"]>().mockRejectedValue(new Error("down")) }), () => true, s);
    expect(s.commits).toHaveBeenCalledWith([], false);
    expect(s.commitsDone).toHaveBeenCalledOnce();
  });

  it("skips commits without a branch", async () => {
    const a = api();
    const s = sink();
    await loadGitView(view({ mode: "commits", commitBranch: "" }), a, () => true, s);
    expect(a.fetchCommits).not.toHaveBeenCalled();
    expect(s.commitsStarted).not.toHaveBeenCalled();
  });

  it.each([
    { name: "page", settle: (p: ReturnType<typeof deferred<CommitEntry[]>>) => p.resolve([commit(1)]) },
    { name: "failure", settle: (p: ReturnType<typeof deferred<CommitEntry[]>>) => p.reject(new Error("down")) },
  ])("drops a commits $name that lands after the view changed", async ({ settle }) => {
    const pending = deferred<CommitEntry[]>();
    const s = sink();
    let current = true;
    const done = loadGitView(view({ mode: "commits" }), api({ fetchCommits: () => pending.promise }), () => current, s);
    current = false;
    settle(pending);
    await done;
    expect(s.commitsStarted).toHaveBeenCalledOnce();
    expect(s.commits).not.toHaveBeenCalled();
    expect(s.commitsDone).not.toHaveBeenCalled();
  });
});
