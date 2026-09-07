/** How the composer delivers text while a run is active. */
export type SendMode = "queue" | "interrupt";

/**
 * Where a composer send has to go.
 *
 * A channel parked on an AskUserQuestion or ExitPlanMode card is not draining
 * its queue: the backend claims no rows until the park is cleared through the
 * matching resolve endpoint. Text delivered with a plain sendMessage while
 * parked therefore sits in the queue forever — and, because the card is the
 * only way to clear the park, the user is left with nothing to answer.
 *
 * Every composer path (typed text, prompt shortcuts) asks this function where
 * to send instead of deciding for itself, so no path can grow its own idea of
 * what a parked channel means.
 */
export type SendRoute = { kind: "ask" } | { kind: "plan" } | { kind: "gate"; reqId: string } | { kind: "message"; interrupt: boolean };

export interface SendRouteInput {
  /** Channel is parked on an AskUserQuestion card. */
  hasPendingAskUser?: boolean;
  /** Channel is parked on an ExitPlanMode card. */
  hasPendingExitPlan?: boolean;
  /** req_id of a chat-sourced gate approval still awaiting a decision. */
  pendingGateReqId?: string | null;
  /** A run is currently active on the channel. */
  isRunning?: boolean;
  /** Composer send mode — only consulted while a run is active. */
  sendMode?: SendMode;
}

export function chooseSendRoute({ hasPendingAskUser, hasPendingExitPlan, pendingGateReqId, isRunning, sendMode }: SendRouteInput): SendRoute {
  // Parks come first, ask before plan — the same precedence the chat uses when
  // rendering the cards, so the send resolves whichever card the user sees.
  if (hasPendingAskUser) return { kind: "ask" };
  if (hasPendingExitPlan) return { kind: "plan" };
  if (pendingGateReqId) return { kind: "gate", reqId: pendingGateReqId };
  return { kind: "message", interrupt: Boolean(isRunning) && sendMode === "interrupt" };
}
