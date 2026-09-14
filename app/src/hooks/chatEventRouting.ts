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
