import { getApiUrl } from "./api";

// ── Branch operations ──

export interface WorktreeInfo {
  path: string;
  branch: string;
  thread_id?: string;
}

export interface BranchInfo {
  branches: string[];
  current: string;
  worktrees: WorktreeInfo[];
}

export async function fetchBranches(channelId: string): Promise<BranchInfo> {
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/branches`);
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

export interface DiffFile {
  path: string;
  old_path?: string; // set when file was renamed
  additions: number;
  deletions: number;
  binary: boolean;
}

export interface DiffResponse {
  files: DiffFile[];
  diff: string;
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

export async function fetchCommits(channelId: string, branch?: string, limit?: number, skip?: number): Promise<CommitEntry[]> {
  const params = new URLSearchParams();
  if (branch) params.set("branch", branch);
  if (limit) params.set("limit", String(limit));
  if (skip) params.set("skip", String(skip));
  const qs = params.toString();
  const res = await fetch(`${getApiUrl()}/api/channels/${channelId}/commits${qs ? `?${qs}` : ""}`);
  if (!res.ok) throw new Error(`Failed to fetch commits: ${res.statusText}`);
  const data: { commits: CommitEntry[] } = await res.json();
  return data.commits;
}

export async function fetchDiff(channelId: string, source?: string, target?: string): Promise<DiffResponse> {
  let url = `${getApiUrl()}/api/channels/${channelId}/diff`;
  if (source && target) {
    url += `?source=${encodeURIComponent(source)}&target=${encodeURIComponent(target)}`;
  }
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch diff: ${res.statusText}`);
  return res.json();
}
