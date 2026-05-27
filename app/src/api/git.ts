import { getApiUrl } from "./api";

// ── Branch operations ──

export interface WorktreeInfo {
  path: string;
  branch: string;
  thread_id?: string;
  locked?: boolean;
}

export interface BranchInfo {
  branches: string[];
  current: string;
  worktrees: WorktreeInfo[];
}

export async function fetchBranches(channelId: string, rootIndex?: number): Promise<BranchInfo> {
  const qs = rootIndex !== undefined && rootIndex > 0 ? `?root=${rootIndex}` : "";
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/branches${qs}`);
  if (!res.ok) throw new Error(`Failed to fetch branches: ${res.statusText}`);
  return res.json();
}

export async function switchBranch(channelId: string, branch: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/branches/switch`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ branch }),
  });
  if (!res.ok) throw new Error(await res.text());
}

export async function deleteBranch(channelId: string, branch: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/branches`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ branch }),
  });
  if (!res.ok) throw new Error(await res.text());
}

export async function createBranch(channelId: string, name: string, from?: string): Promise<void> {
  const body: Record<string, string> = { name };
  if (from) body.from = from;
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/branches/create`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to create branch: ${res.statusText}`);
}

// ── Diff & Commits ──

export type DiffFileStatus = "staged" | "unstaged" | "untracked" | "conflict";

export interface DiffFile {
  path: string;
  old_path?: string; // set when file was renamed
  additions: number;
  deletions: number;
  binary: boolean;
  // Absent for branch-to-branch diff entries.
  status?: DiffFileStatus;
}

export interface DiffResponse {
  files: DiffFile[];
  // Concatenated patch text — used as a change-detection fingerprint and
  // as the only diff source for branch-to-branch mode.
  diff: string;
  // Per-status patches for uncommitted mode. Empty/undefined for branch
  // mode. Parsed separately so partially-staged files (path appearing as
  // both staged and unstaged) get distinct ParsedFile entries.
  staged_diff?: string;
  unstaged_diff?: string;
  conflict_diff?: string;
  untracked_diff?: string;
  total_additions: number;
  total_deletions: number;
}

export interface CommitEntry {
  hash: string;
  short: string;
  subject: string;
  author: string;
  date: string;
}

export async function fetchCommits(channelId: string, branch?: string, limit?: number, skip?: number, rootIndex?: number): Promise<CommitEntry[]> {
  const params = new URLSearchParams();
  if (branch) params.set("branch", branch);
  if (limit) params.set("limit", String(limit));
  if (skip) params.set("skip", String(skip));
  if (rootIndex !== undefined && rootIndex > 0) params.set("root", String(rootIndex));
  const qs = params.toString();
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/commits${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch commits: ${res.statusText}`);
  const data: { commits: CommitEntry[] } = await res.json();
  return data.commits;
}

// ── Pull request ──

export interface PRInfo {
  number: number;
  url: string;
  base_ref: string;
  head_ref: string;
  state: string;
  title?: string;
  is_draft?: boolean;
}

export interface PRResponse {
  present: boolean;
  pr?: PRInfo;
}

export async function fetchPR(channelId: string): Promise<PRResponse> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/pr`);
  if (!res.ok) throw new Error(`Failed to fetch PR info: ${res.statusText}`);
  return res.json();
}

export async function fetchDiff(channelId: string, source?: string, target?: string, rootIndex?: number): Promise<DiffResponse> {
  const params = new URLSearchParams();
  if (source && target) {
    params.set("source", source);
    params.set("target", target);
  }
  if (rootIndex !== undefined && rootIndex > 0) {
    params.set("root", String(rootIndex));
  }
  const qs = params.toString();
  const url = qs ? `${getApiUrl()}/api/channels/${channelId}/diff?${qs}` : `${getApiUrl()}/api/channels/${channelId}/diff`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch diff: ${res.statusText}`);
  return res.json();
}
