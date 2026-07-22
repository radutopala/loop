// Sidebar status-pill registry. One row per pill kind: the store tracks
// membership in a single Map<PillKind, Set<channelId>> and the sidebar
// renders whatever kinds are lit — adding a pill is a union member here
// plus a config row, with no new refs or props anywhere.

/** The pill kinds the chat store tracks per channel. */
export type PillKind = "gate" | "rev" | "ask" | "plan";

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
];
