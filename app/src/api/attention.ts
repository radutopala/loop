import type { AskUserQuestion } from "../types";
import { apiFetch, getApiUrl } from "./api";
import { formatAskAnswers, resolveAsk, resolvePlan } from "./channels";
import { GateApprovalGoneError, resolveGateApproval } from "./gate";
import { deleteNotification } from "./notifications";

/** The event telling the app that what waits on the owner changed. */
export const ATTENTION_CHANGED = "attention.changed";

/** One thing waiting on the owner, as GET /api/attention lists it. */
export interface AttentionItem {
  kind: "ask" | "plan" | "gate" | "pr" | "run" | string;
  channel_id: string;
  channel_name?: string;
  /** The parent channel's name, for a thread. */
  parent_name?: string;
  title: string;
  detail?: string;
  /** Identifies the item within its kind and channel. */
  ref?: string;
  /** Whether an agent is held until the owner acts. */
  blocking: boolean;
  /** A question item's questions. */
  questions?: AskUserQuestion[];
}

/** What waits on the owner across every project, the blocking items first. */
export async function fetchAttention(): Promise<AttentionItem[]> {
  const res = await apiFetch(`${getApiUrl()}/api/attention`);
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(`Failed to list what needs you: ${body || res.statusText}`);
  }
  const body: { items: AttentionItem[] | null } = await res.json();
  return body.items ?? [];
}

/** An item's key, stable while it waits. */
export function attentionKey(it: AttentionItem): string {
  return `${it.kind}\u0000${it.channel_id}\u0000${it.ref ?? ""}`;
}

/** A channel's items, under the channel's label. */
export interface AttentionGroup {
  channelId: string;
  label: string;
  items: AttentionItem[];
}

/** The items grouped by channel, in the order the channels first appear. */
export function groupAttention(items: AttentionItem[]): AttentionGroup[] {
  const groups = new Map<string, AttentionGroup>();
  for (const it of items) {
    let g = groups.get(it.channel_id);
    if (!g) {
      const name = it.channel_name || it.channel_id;
      g = { channelId: it.channel_id, label: it.parent_name ? `${it.parent_name} › ${name}` : name, items: [] };
      groups.set(it.channel_id, g);
    }
    g.items.push(it);
  }
  return [...groups.values()];
}

/**
 * Whether the tray should flash: a blocking item the owner hasn't seen in
 * the inbox yet.
 */
export function needsFlash(items: AttentionItem[], seen: ReadonlySet<string>): boolean {
  return items.some((it) => it.blocking && !seen.has(attentionKey(it)));
}

/** The seen keys still waiting, plus every item's when the inbox is shown. */
export function nextSeen(items: AttentionItem[], seen: ReadonlySet<string>, shown: boolean): Set<string> {
  const keys = items.map(attentionKey);
  return new Set(shown ? keys : keys.filter((k) => seen.has(k)));
}

/** Something the inbox can do about an item without opening its channel. */
export interface AttentionAction {
  id: string;
  label: string;
  variant?: "primary" | "danger";
}

/**
 * The item's actions, the same the chat's cards offer. A question is
 * answered here only when it is one pick from a list; anything more (several
 * questions, several picks, written changes to a plan) opens the channel.
 */
export function attentionActions(it: AttentionItem): AttentionAction[] {
  switch (it.kind) {
    case "gate":
      return [
        { id: "once", label: "Allow once", variant: "primary" },
        { id: "session", label: "Allow for session" },
        { id: "deny", label: "Deny", variant: "danger" },
      ];
    case "plan":
      return [
        { id: "approve", label: "Approve & Execute", variant: "primary" },
        { id: "reject", label: "Discard" },
      ];
    case "ask": {
      const q = it.questions?.length === 1 ? it.questions[0]! : undefined;
      if (!q?.options?.length || q.multiSelect) return [];
      return [...q.options.map((o, i) => ({ id: `option:${i}`, label: o.label })), { id: "skip", label: "Skip" }];
    }
    case "run":
      return [{ id: "dismiss", label: "Dismiss" }];
    default:
      return [];
  }
}

/** Takes one of attentionActions(it). A gate request already gone is done. */
export async function runAttentionAction(it: AttentionItem, id: string): Promise<void> {
  switch (it.kind) {
    case "gate":
      try {
        await resolveGateApproval(it.ref ?? "", id as "once" | "session" | "deny");
      } catch (err) {
        if (!(err instanceof GateApprovalGoneError)) throw err;
      }
      return;
    case "plan":
      return resolvePlan(it.channel_id, id as "approve" | "reject");
    case "ask": {
      if (id === "skip") return resolveAsk(it.channel_id, "skip");
      const q = it.questions![0]!;
      const label = q.options![Number(id.slice("option:".length))]!.label;
      return resolveAsk(it.channel_id, "answer", formatAskAnswers([{ question: q.question, labels: [label] }]));
    }
    case "run":
      return deleteNotification(Number(it.ref));
  }
}
