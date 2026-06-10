import type { TimelineItem } from "../../types";

// orderTimelineItems moves each triggered user message to sit just before its
// first reply, so a *queued* message renders with its run at the position where
// it was PROCESSED rather than where it was enqueued. A queued message is
// inserted at enqueue time (an early slot) but its replies arrive after whatever
// turn was already running; left in place, groupTimelineItems would pull its
// reply group up in front of that intervening turn. The rest of the list keeps
// its order — the base list is already correct (backend chain order on reload;
// single-turn arrival order live), so this only repositions the user row.
//
// Replies are matched by trigger_msg_id. A user message whose replies aren't in
// the loaded window (e.g. still queued, or its run hasn't started) has no match
// and stays put; one whose trigger is off-page is simply absent and its replies
// render where the base order placed them — identical to groupTimelineItems'
// existing handling of off-page triggers.
export function orderTimelineItems(list: TimelineItem[]): TimelineItem[] {
  const firstReplyIdx = new Map<string, number>();
  for (let i = 0; i < list.length; i++) {
    const trig = list[i]!.trigger_msg_id;
    if (trig && !firstReplyIdx.has(trig)) firstReplyIdx.set(trig, i);
  }
  // For each user message with a later first reply, queue it to be inserted
  // just before that reply, and skip its original slot.
  const insertBefore = new Map<number, TimelineItem[]>();
  const relocated = new Set<number>();
  for (let i = 0; i < list.length; i++) {
    const it = list[i]!;
    if (it.kind !== "message" || it.data.is_bot) continue;
    const ri = firstReplyIdx.get(it.data.msg_id);
    if (ri === undefined || ri <= i) continue;
    (insertBefore.get(ri) ?? insertBefore.set(ri, []).get(ri)!).push(it);
    relocated.add(i);
  }
  if (relocated.size === 0) return list;
  const out: TimelineItem[] = [];
  for (let i = 0; i < list.length; i++) {
    const pending = insertBefore.get(i);
    if (pending) out.push(...pending);
    if (relocated.has(i)) continue;
    out.push(list[i]!);
  }
  return out;
}
