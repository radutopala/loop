import type { Channel, PluginBadge } from "../../types";

// Sidebar status-pill registry. One row per pill kind: the store tracks
// membership in a single Map<PillKind, Set<channelId>> and the sidebar
// renders whatever kinds are lit — adding a pill is a union member here
// plus a config row, with no new refs or props anywhere.

/** The pill kinds the chat store tracks per channel. */
export type PillKind = "gate" | "rev" | "ask" | "plan" | "trust" | "ci" | "conflict" | "cmt";

export interface PillSpec {
  kind: PillKind;
  label: string;
  /** Theme color key on the `colors` object. */
  color: "warning" | "active" | "error";
}

/** Render order matches the pre-unification hardcoded order. */
export const SIDEBAR_PILLS: PillSpec[] = [
  { kind: "gate", label: "gate", color: "warning" },
  { kind: "rev", label: "rev", color: "active" },
  { kind: "ask", label: "ask", color: "warning" },
  { kind: "plan", label: "plan", color: "warning" },
  { kind: "trust", label: "trust", color: "warning" },
  { kind: "ci", label: "ci", color: "error" },
  { kind: "conflict", label: "cnf", color: "error" },
  { kind: "cmt", label: "cmt", color: "warning" },
];

/**
 * The pill a channel shows while a review runs on it. It isn't a PillKind: a
 * running review isn't waiting on you, so the needs-you filter and Recent's
 * waiting group leave it out.
 */
export const REVIEWING_PILL: Omit<PillSpec, "kind"> = { label: "rev…", color: "active" };

/**
 * The rows the trust pill lights: a project's config waiting for trust is one
 * pill, on its top-level row. Its threads and worktrees report it too, but
 * show the chat banner instead, so one project doesn't fill the sidebar.
 */
export function trustPillIds(channels: ReadonlyArray<Pick<Channel, "id" | "parent_id" | "trust_pending">>): Set<string> {
  return new Set(channels.filter((c) => c.trust_pending && !c.parent_id).map((c) => c.id));
}

/**
 * The pill a thread shows while its agent was sent to fix its CI. Like
 * REVIEWING_PILL it isn't a PillKind: it's being handled, not waiting on you.
 */
export function fixingPill(attention: Channel["pr_attention"]): Omit<PillSpec, "kind"> | undefined {
  if (attention === "fixing") return { label: "ci…", color: "active" };
  return undefined;
}

/** The rows the ci pill lights: a CI failure no agent is fixing. */
export function ciPillIds(channels: ReadonlyArray<Pick<Channel, "id" | "pr_attention">>): Set<string> {
  return new Set(channels.filter((c) => c.pr_attention === "failed").map((c) => c.id));
}

/** The rows the conflict pill lights: a merge conflict, which is always yours to resolve. */
export function conflictPillIds(channels: ReadonlyArray<Pick<Channel, "id" | "pr_attention">>): Set<string> {
  return new Set(channels.filter((c) => c.pr_attention === "conflict").map((c) => c.id));
}

/** The rows the cmt pill lights: a PR/MR with unresolved review comments. */
export function commentPillIds(channels: ReadonlyArray<Pick<Channel, "id" | "unresolved_threads">>): Set<string> {
  return new Set(channels.filter((c) => (c.unresolved_threads ?? 0) > 0).map((c) => c.id));
}

/** A plugin badge's tooltip: the plugin, then its title or else its text. */
export function pluginBadgeTitle(b: PluginBadge): string {
  return `${b.plugin_id}: ${b.title || b.text}`;
}
