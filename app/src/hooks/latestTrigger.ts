import type { TimelineItem } from "../types";

// latestTrigger is the trigger_msg_id of the last item in list that has one,
// or undefined. A message the agent took into its running turn links to it:
// the latest linked row is the turn that read the message.
export function latestTrigger(list: TimelineItem[]): string | undefined {
  for (let i = list.length - 1; i >= 0; i--) {
    const trigger = list[i]!.trigger_msg_id;
    if (trigger) return trigger;
  }
  return undefined;
}
