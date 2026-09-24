import type { Channel } from "../../types";

/** One line of a sidebar row's info popup. */
export interface RowInfoLine {
  key: "path" | "branch" | "commit" | "sync" | "model" | "status";
  value: string;
  /** Shown dimmed after the value, e.g. the commit's subject. */
  detail?: string;
}

/**
 * The lines of a sidebar row's info popup, leaving out any with nothing to
 * say: its directory, its branch, its
 * commit and subject, how far it is from its base and upstream branches, its
 * model/effort overrides, and whether its agent is running or it's locked.
 */
export function rowInfoLines(channel: Channel): RowInfoLine[] {
  const lines: RowInfoLine[] = [];
  if (channel.dir_path) lines.push({ key: "path", value: channel.dir_path });
  const commit = channel.commit?.slice(0, 7);
  if (channel.branch && channel.branch !== "HEAD") {
    const from = channel.worktree && channel.base_branch ? { detail: `(from ${channel.base_branch})` } : {};
    lines.push({ key: "branch", value: channel.branch, ...from });
  } else if (commit) {
    lines.push({ key: "branch", value: "detached" });
  }
  if (commit) lines.push({ key: "commit", value: commit, ...(channel.subject ? { detail: channel.subject } : {}) });
  const sync = [channel.sync_base ? distance(channel.base_ahead, channel.base_behind, channel.sync_base) : "", channel.upstream ? distance(channel.ahead, channel.behind, channel.upstream) : ""]
    .filter(Boolean)
    .join(" · ");
  if (sync) lines.push({ key: "sync", value: sync });
  const model = [channel.model_override, channel.effort_override].filter(Boolean).join(" · ");
  if (model) lines.push({ key: "model", value: model });
  const status = [channel.agent_running ? "agent running" : channel.container_running ? "container running" : "", channel.locked ? "locked" : ""].filter(Boolean).join(" · ");
  if (status) lines.push({ key: "status", value: status });
  return lines;
}

/** How far a branch is from another, e.g. "↑3 ↓1 vs main" or "even with main". */
function distance(ahead = 0, behind = 0, other: string): string {
  const arrows = [ahead ? `↑${ahead}` : "", behind ? `↓${behind}` : ""].filter(Boolean).join(" ");
  return arrows ? `${arrows} vs ${other}` : `even with ${other}`;
}
