import type { LearnPass, LearnProposal, LearnProposalKind } from "../../api/learn";

// The Learn switch shows what applies: the channel's own setting when it has
// one, the config default otherwise. A click stores the opposite explicitly,
// so the channel keeps it whatever the config says later.
export function learnEffective(learn: "" | "on" | "off", defaultLearn: boolean): boolean {
  if (learn === "") return defaultLearn;
  return learn === "on";
}

export function learnToggleTitle(learn: "" | "on" | "off", defaultLearn: boolean): string {
  const on = learnEffective(learn, defaultLearn);
  const source = learn === "" ? `config default (${defaultLearn ? "on" : "off"})` : "set for this channel";
  const what = "After each run, a hidden forked session reviews it and proposes shortcuts, tasks, gate rules, mounts, a thread name, description or ticket link for you to apply.";
  return `Learn is ${on ? "on" : "off"} — ${source}.\n${what}\nClick to turn it ${on ? "off" : "on"}.`;
}

const KIND_LABELS: Record<LearnProposalKind, string> = {
  prompt_shortcut: "prompt shortcut",
  bash_shortcut: "bash shortcut",
  scheduled_task: "scheduled task",
  gate_rule: "gate rule",
  mount: "mount",
  rename: "rename",
  description: "description",
  ticket_url: "ticket",
};

export function learnKindLabel(kind: string): string {
  return KIND_LABELS[kind as LearnProposalKind] ?? kind;
}

// The kinds whose apply appends to the project's .loop/config.json: their
// cards preview the edit as a diff before it's applied.
const CONFIG_KINDS: ReadonlySet<string> = new Set<LearnProposalKind>(["prompt_shortcut", "bash_shortcut", "gate_rule", "mount"]);

export function editsProjectConfig(kind: string): boolean {
  return CONFIG_KINDS.has(kind);
}

// Changes whenever a config-kind proposal is applied, so the open cards'
// previews are worked out again against the edited file.
export function configPreviewRevision(proposals: LearnProposal[]): string {
  return proposals
    .filter((p) => p.status === "applied" && editsProjectConfig(p.kind))
    .map((p) => p.id)
    .join(",");
}

// How long a proposal may sit in "applying" before the server lets it be
// claimed again (db.LearnApplyStale): one still applying by then lost its
// outcome.
export const LEARN_APPLY_STALE_MS = 60_000;

// A proposal done with: applied, dismissed, or withdrawn by a later learn
// pass. Its card stays in the list, dimmed.
export function isSettledProposal(p: LearnProposal): boolean {
  return p.status === "applied" || p.status === "dismissed" || p.status === "withdrawn";
}

// A proposal still waiting on the user: never settled, failed and retryable,
// or stuck applying past LEARN_APPLY_STALE_MS. A withdrawn one isn't.
export function isOpenProposal(p: LearnProposal, now = Date.now()): boolean {
  if (p.status === "pending" || p.status === "failed") return true;
  return p.status === "applying" && now - Date.parse(p.updated_at) > LEARN_APPLY_STALE_MS;
}

// How long until the next proposal stuck applying goes stale (and so open
// again), or null when none will.
export function nextStaleIn(proposals: LearnProposal[], now = Date.now()): number | null {
  const due = proposals
    .filter((p) => p.status === "applying")
    .map((p) => Date.parse(p.updated_at) + LEARN_APPLY_STALE_MS - now)
    .filter((ms) => ms >= 0);
  return due.length === 0 ? null : Math.min(...due);
}

// What Apply all and Dismiss all go through: Apply all applies the pending
// proposals (failed ones are left for a manual Retry), Dismiss all dismisses
// every open one. Each is checked again when its turn comes, so one settled
// meanwhile is left alone.
export type LearnBulk = "apply" | "dismiss";

export function inBulk(bulk: LearnBulk, p: LearnProposal, now = Date.now()): boolean {
  return bulk === "apply" ? p.status === "pending" : isOpenProposal(p, now);
}

// Whether incoming proposals newly applied a prompt or bash shortcut, one
// the current list doesn't have as applied yet, so the pickers that list
// shortcuts should fetch them again. A refetch that brings back shortcuts
// applied long ago doesn't.
export function newlyAppliedShortcut(current: LearnProposal[], incoming: LearnProposal[]): boolean {
  const applied = new Set(current.filter((p) => p.status === "applied").map((p) => p.id));
  return incoming.some((p) => p.status === "applied" && (p.kind === "prompt_shortcut" || p.kind === "bash_shortcut") && !applied.has(p.id));
}

// Whether a learn pass is running in the learn thread after one of its
// agent.status events. Only a pass ("learn") starts it; a reply the user
// asked the thread for ("learn-reply") doesn't, and any run's end stops it
// (the thread runs one at a time).
export function learnPassRunning(cur: boolean, status: string, trigger: string | undefined): boolean {
  if (status !== "running") return false;
  return trigger === "learn" ? true : cur;
}

// The Learn label in the chat pane's header: what the learn pass is doing, else how
// many proposals wait, else null when there's nothing to show.
/**
 * What a turn's Learn action says about the turn's learn pass: its state
 * while it's queued or running, else how many proposals it filed. A turn
 * without one, or whose old pass the next turn's superseded before it ran,
 * offers to learn from it.
 */
export function learnTurnLabel(pass: LearnPass | undefined, proposals: number): string {
  switch (pass?.status) {
    case "queued":
      return "Learn queued…";
    case "running":
      return "Learning…";
    case "failed":
      return "Learn failed";
    case "done":
      return proposals === 0 ? "No proposals" : `${proposals} proposal${proposals === 1 ? "" : "s"}`;
    default:
      return "Learn";
  }
}

/** Folds incoming learn passes (new or updated) into the list, newest first. */
export function mergeLearnPasses(list: LearnPass[], incoming: LearnPass[]): LearnPass[] {
  const byId = new Map(list.map((p) => [p.id, p]));
  for (const p of incoming) byId.set(p.id, p);
  return [...byId.values()].sort((a, b) => b.id - a.id);
}

/** Each reviewed turn's newest learn pass, by the turn's last bot message. */
export function learnPassesByMessage(passes: LearnPass[]): Map<string, LearnPass> {
  const m = new Map<string, LearnPass>();
  for (const p of passes) {
    const cur = m.get(p.message_id);
    if (!cur || p.id > cur.id) m.set(p.message_id, p);
  }
  return m;
}

// The proposals a learn.proposals event carries: the ones the pass filed,
// and the earlier ones it withdrew (a call that only withdraws files none),
// to merge in one go.
export function learnProposalsEventItems(data: { proposals: LearnProposal[]; withdrawn?: LearnProposal[] }): LearnProposal[] {
  return [...data.proposals, ...(data.withdrawn ?? [])];
}

// mergeProposals folds incoming proposals (new or updated) into the list,
// newest first.
export function mergeProposals(list: LearnProposal[], incoming: LearnProposal[]): LearnProposal[] {
  const byId = new Map(list.map((p) => [p.id, p]));
  for (const p of incoming) byId.set(p.id, p);
  return [...byId.values()].sort((a, b) => b.id - a.id);
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// proposalDetail is the one-line gist of a proposal's payload, shown under
// its title so the user sees exactly what Apply writes.
export function proposalDetail(p: LearnProposal): string {
  let v: Record<string, unknown>;
  try {
    v = JSON.parse(p.payload) as Record<string, unknown>;
  } catch {
    return p.payload;
  }
  switch (p.kind) {
    case "prompt_shortcut":
      return `#${str(v.name)} → ${str(v.prompt)}`;
    case "bash_shortcut":
      return `${str(v.name)} → $ ${str(v.command)}`;
    case "scheduled_task": {
      const body = v.bash_script ? `$ ${str(v.bash_script)}` : str(v.prompt);
      return `${str(v.type)} ${str(v.schedule)} → ${body}`;
    }
    case "gate_rule":
      return `${str(v.type)} rule: ${gateRuleDetail(str(v.type), (v.rule ?? {}) as Record<string, unknown>)}`.trim();
    case "mount":
      return str(v.mount);
    case "rename":
      return `→ ${str(v.name)}`;
    case "description":
      return str(v.description);
    case "ticket_url":
      return str(v.ticket_url);
  }
  return p.payload;
}

function strs(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
}

// A gate rule in full, so Apply writes nothing the card didn't show: its
// decision, what it matches (an empty list matches anything), and the
// message the gate shows. A rule of a type this doesn't know shows as JSON.
function gateRuleDetail(type: string, rule: Record<string, unknown>): string {
  let subject: string;
  switch (type) {
    case "command": {
      const commands = strs(rule.commands);
      const args = strs(rule.args_patterns);
      subject = `${commands.length ? commands.join(", ") : "any command"}${args.length ? ` with args matching ${args.join(" | ")}` : ""}`;
      break;
    }
    case "file": {
      const paths = strs(rule.paths);
      const ops = strs(rule.operations);
      subject = `${paths.length ? paths.join(", ") : "any path"}${ops.length ? ` on ${ops.join(", ")}` : ""}`;
      break;
    }
    case "path":
      subject = str(rule.pattern);
      break;
    default:
      return JSON.stringify(rule);
  }
  const message = str(rule.message);
  return [str(rule.decision), subject, message && `— “${message}”`].filter(Boolean).join(" ");
}

// Renaming a worktree thread renames the thread only; the branch and the
// worktree's folder keep theirs.
export function proposalCaveat(p: LearnProposal, worktree: boolean): string | null {
  if (p.kind === "rename" && worktree) return "Renames the thread only; its branch and worktree folder keep their names.";
  return null;
}
