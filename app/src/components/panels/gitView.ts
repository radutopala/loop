import type { CommitEntry, DiffResponse } from "../../api/loopApi";
import type { ParsedFile } from "./DiffViewer";
import { parseUnifiedDiff } from "./DiffViewer";

export type GitMode = "uncommitted" | "branches" | "commits" | "worktrees" | "branchlist";

export const COMMITS_PAGE = 50;

/** What the Git panel shows: a load answers for the view it started in. */
export interface GitView {
  channelId: string;
  mode: GitMode;
  sourceBranch: string;
  targetBranch: string;
  commitBranch: string;
  rootIndex: number;
}

export function gitViewKey(v: GitView): string {
  return [v.channelId, v.mode, v.sourceBranch, v.targetBranch, v.commitBranch, v.rootIndex].join("\0");
}

export interface GitViewApi {
  fetchCommits(channelId: string, branch?: string, limit?: number, skip?: number, rootIndex?: number): Promise<CommitEntry[]>;
  fetchDiff(channelId: string, source?: string, target?: string, rootIndex?: number): Promise<DiffResponse>;
}

/** Where a load's results go. */
export interface GitViewSink {
  commitsStarted(): void;
  commits(c: CommitEntry[], hasMore: boolean): void;
  commitsDone(): void;
  diff(d: DiffResponse, files: ParsedFile[]): void;
  diffDone(): void;
}

/**
 * Loads a view's commits or diff into sink. Once current() turns false the
 * view on screen changed (mode, branches, root, channel), so a late answer,
 * e.g. a slow branch diff landing after a switch to uncommitted, is dropped.
 */
export async function loadGitView(v: GitView, api: GitViewApi, current: () => boolean, sink: GitViewSink): Promise<void> {
  if (v.mode === "commits") {
    if (!v.commitBranch) return;
    sink.commitsStarted();
    try {
      const c = await api.fetchCommits(v.channelId, v.commitBranch, COMMITS_PAGE, 0, v.rootIndex);
      if (!current()) return;
      sink.commits(c, c.length >= COMMITS_PAGE);
    } catch {
      if (!current()) return;
      sink.commits([], false);
    } finally {
      if (current()) sink.commitsDone();
    }
    return;
  }
  // In branch mode, both branches must be selected
  if (v.mode === "branches" && (!v.sourceBranch || !v.targetBranch)) return;
  try {
    const d = v.mode === "branches" ? await api.fetchDiff(v.channelId, v.sourceBranch, v.targetBranch, v.rootIndex) : await api.fetchDiff(v.channelId, undefined, undefined, v.rootIndex);
    if (!current()) return;
    if (v.mode === "branches") {
      // Branch-to-branch diff has no staged/unstaged distinction.
      sink.diff(d, parseUnifiedDiff(d.diff));
    } else {
      // Uncommitted mode: parse each section with its known status so a
      // partially-staged file produces two distinct ParsedFile entries.
      sink.diff(d, [
        ...parseUnifiedDiff(d.staged_diff ?? "", "staged"),
        ...parseUnifiedDiff(d.unstaged_diff ?? "", "unstaged"),
        ...parseUnifiedDiff(d.conflict_diff ?? "", "conflict"),
        ...parseUnifiedDiff(d.untracked_diff ?? "", "untracked"),
      ]);
    }
  } catch {
    /* ignore fetch errors — will retry on next poll */
  } finally {
    if (current()) sink.diffDone();
  }
}
