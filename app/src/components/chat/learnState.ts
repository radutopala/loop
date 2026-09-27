import type { LearnProposal, LearnProposalKind } from "../../api/learn";

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

// How long a proposal may sit in "applying" before the server lets it be
// claimed again (db.LearnApplyStale): one still applying by then lost its
// outcome.
export const LEARN_APPLY_STALE_MS = 60_000;

// A proposal still waiting on the user: never settled, failed and retryable,
// or stuck applying past LEARN_APPLY_STALE_MS.
export function isOpenProposal(p: LearnProposal, now = Date.now()): boolean {
  if (p.status === "pending" || p.status === "failed") return true;
  return p.status === "applying" && now - Date.parse(p.updated_at) > LEARN_APPLY_STALE_MS;
}

// The Learn label in the chat pane's header: what the learn pass is doing, else how
// many proposals wait, else null when there's nothing to show.
export function learnBadgeLabel(running: boolean, open: number): string | null {
  if (running) return "learning…";
  if (open > 0) return `${open} proposal${open === 1 ? "" : "s"}`;
  return null;
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
    case "gate_rule": {
      const rule = (v.rule ?? {}) as Record<string, unknown>;
      const subject = [rule.commands, rule.paths, rule.pattern].flat().filter((x): x is string => typeof x === "string");
      return `${str(v.type)} rule: ${str(rule.decision)} ${subject.join(", ")}`.trim();
    }
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

// Renaming a worktree thread renames the thread only; the branch and the
// worktree's folder keep theirs.
export function proposalCaveat(p: LearnProposal, worktree: boolean): string | null {
  if (p.kind === "rename" && worktree) return "Renames the thread only; its branch and worktree folder keep their names.";
  return null;
}
