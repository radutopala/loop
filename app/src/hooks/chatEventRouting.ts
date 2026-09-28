/**
 * Whether a WS event should be handed to the chat-event listeners registered
 * through `subscribeChatEvents`. Pure — extracted from useChatStateStore so
 * the routing rule can be unit-tested without a live socket.
 *
 * Channel-scoped events reach only the selected channel's listeners: that is
 * what keeps a background channel's stream out of the open view. `stateTarget`
 * (not the raw `channel_id`) is the subject, so an `agent.status` routed to a
 * thread doesn't light up its parent.
 *
 * `workflow.*` is the exception, because those are broadcast **globally** and
 * arrive with an empty envelope `channel_id` — a run's channel lives in the
 * run payload, and node events carry none at all. Tested against
 * `selectedIdRef` they are dropped every time, which left the Review panel
 * blind to `workflow.run_completed`: its loop chip stayed on "running" and
 * `loopActive` never cleared, so Run review and the chat composer stayed
 * disabled after a run had finished. Every subscriber filters on `run_id`,
 * so forwarding another channel's run is inert.
 */
export function shouldForwardToChatListeners(eventType: string, stateTarget: string, selectedId: string | null): boolean {
  if (eventType.startsWith("workflow.")) return true;
  return stateTarget !== "" && stateTarget === selectedId;
}

/**
 * The channels the WS should be subscribed to: the selected one, every one
 * with a run in flight, and every one a panel watches through
 * `subscribeChannelEvents` (e.g. the Learn view's hidden learn thread,
 * which is never selected). Sorted, so equal sets give equal keys.
 */
export function subscriptionChannels(selectedId: string | null | undefined, running: Iterable<string>, watched: Iterable<string>): string[] {
  const set = new Set<string>();
  if (selectedId) set.add(selectedId);
  for (const id of running) set.add(id);
  for (const id of watched) set.add(id);
  return [...set].sort();
}

/**
 * Whether a finished run should mark its channel unread and post a desktop
 * notification. A learn pass runs in a hidden thread the user can't open
 * from the sidebar; its proposals surface through the Learn badge instead.
 */
export function alertsOnRunEnd(trigger: string | undefined): boolean {
  return trigger !== "learn";
}

/**
 * Whether a finished run should bounce the dock. Only runs the user started
 * do: scheduled tasks fire often, "bot" runs are indirect chains (an agent
 * re-entering via the send_message / create_thread MCP tools) and learn
 * passes are background reviews.
 */
export function bouncesOnRunEnd(trigger: string | undefined): boolean {
  return trigger !== "scheduled" && trigger !== "bot" && trigger !== "learn";
}
