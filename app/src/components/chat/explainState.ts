import type { Explanation } from "../../api/explain";
import type { Message } from "../../types";
import { learnEffective } from "./learnState";

export function explainToggleTitle(explain: "" | "on" | "off", defaultExplain: boolean): string {
  const on = learnEffective(explain, defaultExplain);
  const source = explain === "" ? `config default (${defaultExplain ? "on" : "off"})` : "set for this channel";
  const what = "After each run, a hidden forked session explains the turn: what changed, the commands it ran, the decisions, risks and how to verify. The write-ups are in the Explain view.";
  return `Explain is ${on ? "on" : "off"} — ${source}.\n${what}\nClick to turn it ${on ? "off" : "on"}.`;
}

/**
 * Folds incoming explanations (new or updated) into the list, one per
 * explained message, newest first. An incoming one without the turn's
 * snippets (an explain.updated event) keeps the ones already known.
 */
export function mergeExplanations(list: Explanation[], incoming: Explanation[]): Explanation[] {
  const byMsg = new Map(list.map((e) => [e.message_id, e]));
  for (const e of incoming) {
    const cur = byMsg.get(e.message_id);
    byMsg.set(e.message_id, {
      ...e,
      message_row_id: e.message_row_id || cur?.message_row_id,
      prompt: e.prompt || cur?.prompt,
      reply: e.reply || cur?.reply,
    });
  }
  return [...byMsg.values()].sort((a, b) => b.id - a.id);
}

/** Whether an explanation is still being written (queued or running). */
export function explanationPending(e: Explanation | undefined): boolean {
  return e?.status === "queued" || e?.status === "running";
}

/**
 * What a bot bubble's Explain action does: "explain" starts an explanation
 * (none yet), "open" shows the one there is in the Explain pane, "pending"
 * shows it too while it's being written.
 */
export type ExplainAction = "explain" | "open" | "pending";

export function explainAction(e: Explanation | undefined): ExplainAction {
  if (!e) return "explain";
  return explanationPending(e) ? "pending" : "open";
}

export function explainActionLabel(action: ExplainAction, e: Explanation | undefined): string {
  switch (action) {
    case "explain":
      return "Explain";
    case "pending":
      return e?.status === "running" ? "Explaining…" : "Queued…";
    case "open":
      return e?.status === "failed" ? "Explain failed" : "Explained";
  }
}

/**
 * The msg_ids of the bot messages that end a turn: for each turn (the bot
 * messages sharing a trigger_msg_id), its last stored one. A turn still
 * running (processingMsgId, its trigger) has no end yet, nor do bot
 * messages that aren't part of a turn (no trigger) or weren't stored.
 */
export function turnEndMsgIds(messages: Message[], processingMsgId: string | null): Set<string> {
  const last = new Map<string, Message>();
  for (const m of messages) {
    if (!m.is_bot || !m.trigger_msg_id || !(m.id > 0)) continue;
    const cur = last.get(m.trigger_msg_id);
    if (!cur || m.id > cur.id) last.set(m.trigger_msg_id, m);
  }
  const ids = new Set<string>();
  for (const [trigger, m] of last) {
    if (trigger !== processingMsgId) ids.add(m.msg_id);
  }
  return ids;
}

/** A one-line start of a turn's prompt or reply for a card's header. */
export function snippetLine(text: string | undefined, max = 140): string {
  const line = (text ?? "").replace(/\s+/g, " ").trim();
  return line.length > max ? `${line.slice(0, max).trimEnd()}…` : line;
}
