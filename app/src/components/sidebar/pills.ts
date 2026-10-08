import type { Channel } from "../../types";

// Sidebar status-pill registry. One row per pill kind: the store tracks
// membership in a single Map<PillKind, Set<channelId>> and the sidebar
// renders whatever kinds are lit — adding a pill is a union member here
// plus a config row, with no new refs or props anywhere.

/** The pill kinds the chat store tracks per channel. */
export type PillKind = "gate" | "rev" | "ask" | "plan" | "trust";

export interface PillSpec {
  kind: PillKind;
  label: string;
  /** Theme color key on the `colors` object. */
  color: "warning" | "active";
  title: string;
}

/** Render order matches the pre-unification hardcoded order. */
export const SIDEBAR_PILLS: PillSpec[] = [
  { kind: "gate", label: "gate", color: "warning", title: "Approval needed" },
  { kind: "rev", label: "rev", color: "active", title: "Review session open" },
  { kind: "ask", label: "ask", color: "warning", title: "Agent is asking a question" },
  { kind: "plan", label: "plan", color: "warning", title: "Plan awaiting approval" },
  { kind: "trust", label: "trust", color: "warning", title: "Project config changed: review and trust it in Settings → Project" },
];

/**
 * The pill a channel shows while a review runs on it. It isn't a PillKind: a
 * running review isn't waiting on you, so the needs-you filter and Recent's
 * waiting group leave it out.
 */
export const REVIEWING_PILL: Omit<PillSpec, "kind"> = { label: "rev…", color: "active", title: "Review in progress" };

/**
 * The rows the trust pill lights: a project's config waiting for trust is one
 * pill, on its top-level row. Its threads and worktrees report it too, but
 * show the chat banner instead, so one project doesn't fill the sidebar.
 */
export function trustPillIds(channels: ReadonlyArray<Pick<Channel, "id" | "parent_id" | "trust_pending">>): Set<string> {
  return new Set(channels.filter((c) => c.trust_pending && !c.parent_id).map((c) => c.id));
}
