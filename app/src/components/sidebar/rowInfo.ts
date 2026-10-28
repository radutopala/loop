import type { PRInfo } from "../../api/git";
import type { PRStatus } from "../../api/pr";
import type { Channel } from "../../types";
import { prRef, prState } from "../../utils/prState";
import { prChip, prReasonText } from "../../utils/prStatus";

/** One line of a sidebar row's info popup. */
export interface RowInfoLine {
  key: "ticket" | "path" | "branch" | "pr" | "ci" | "conflict" | "cmt" | "commit" | "sync" | "model" | "status";
  value: string;
  /** Shown dimmed after the value, e.g. the commit's subject. */
  detail?: string;
}

/**
 * The lines of a sidebar row's info popup, leaving out any with nothing to
 * say: its ticket's URL, its directory, its branch, the PR open on it, why
 * its ci, cnf and cmt pills are lit (with the watcher's status when given), its
 * commit and subject, how far it is from its base and upstream branches, its
 * model/effort overrides, and whether its agent is running or it's locked.
 */
export function rowInfoLines(channel: Channel, pr?: PRInfo | null, prStatus?: PRStatus | null): RowInfoLine[] {
  const lines: RowInfoLine[] = [];
  const ticket = channel.ticket_url?.trim();
  if (ticket) lines.push({ key: "ticket", value: ticket });
  if (channel.dir_path) lines.push({ key: "path", value: channel.dir_path });
  const commit = channel.commit?.slice(0, 7);
  if (channel.branch && channel.branch !== "HEAD") {
    const from = channel.worktree && channel.base_branch ? { detail: `(from ${channel.base_branch})` } : {};
    lines.push({ key: "branch", value: channel.branch, ...from });
  } else if (commit) {
    lines.push({ key: "branch", value: "detached" });
  }
  if (pr) lines.push({ key: "pr", value: prRef(pr), detail: [pr.title, `(${prState(pr, "").label})`].filter(Boolean).join(" ") });
  lines.push(...prAttentionLines(channel, prStatus));
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

/** Why a row's PR/MR pills are lit: its failure, merge conflict and review threads. */
function prAttentionLines(channel: Channel, status?: PRStatus | null): RowInfoLine[] {
  const lines: RowInfoLine[] = [];
  const attention = channel.pr_attention;
  if (attention === "failed" || attention === "fixing") {
    const fixing = attention === "fixing" ? "agent fixing" : "";
    if (status) {
      const names = (status.failed ?? []).map((f) => f.name).join(", ");
      const value = [status.checks.failed > 0 ? prChip(status).text : "", fixing].filter(Boolean).join(" · ") || "failed";
      const detail = [names && `(${names})`, prReasonText(status)].filter(Boolean).join(" ");
      lines.push({ key: "ci", value, ...(detail ? { detail } : {}) });
    } else {
      lines.push({ key: "ci", value: fixing || "failed" });
    }
  }
  if (attention === "conflict") lines.push({ key: "conflict", value: `with ${status?.base_ref || "its base"}`, detail: "yours to resolve" });
  const threads = channel.unresolved_threads ?? 0;
  if (threads > 0) lines.push({ key: "cmt", value: `${threads} unresolved review thread${threads === 1 ? "" : "s"}` });
  return lines;
}

/** How far a branch is from another, e.g. "↑3 ↓1 vs main" or "even with main". */
function distance(ahead = 0, behind = 0, other: string): string {
  const arrows = [ahead ? `↑${ahead}` : "", behind ? `↓${behind}` : ""].filter(Boolean).join(" ");
  return arrows ? `${arrows} vs ${other}` : `even with ${other}`;
}
