import type { MessageDelivery } from "../../api/channels";

/**
 * How the composer delivers text while a run is active: queued behind the run,
 * added to its running turn (answered in that turn, after the current step),
 * or steering it (the run stops, then the text runs).
 */
export type SendMode = MessageDelivery;

/**
 * Reads a persisted send mode, tolerating the name this mode used to have.
 *
 * "steer" was called "interrupt" until the queue grew its own per-row steer
 * action and the two had to read as the same thing. Anyone who picked the mode
 * before that has "interrupt" in local storage; without this they would be
 * silently dropped back to queueing.
 */
export function normalizeSendMode(stored: unknown): SendMode {
  if (stored === "steer" || stored === "interrupt") return "steer";
  return stored === "add" ? "add" : "queue";
}

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
export type SendRoute = { kind: "ask" } | { kind: "plan" } | { kind: "gate"; reqId: string } | { kind: "message"; delivery: SendMode };

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
  // With no run, there is nothing to add to or steer: the message just runs.
  return { kind: "message", delivery: isRunning && sendMode ? sendMode : "queue" };
}

/** The modifiers held with Enter in the composer. */
export interface EnterModifiers {
  metaKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
}

/**
 * The send mode a modified Enter uses for one send, or undefined for a plain
 * Enter, which sends with the chosen mode. ⌘/Ctrl+Enter steers and Alt+Enter
 * adds to the running turn; either one queues when its mode is already the
 * chosen one, so each still offers a way out of it.
 */
export function oneOffSendMode(sendMode: SendMode, { metaKey, ctrlKey, altKey }: EnterModifiers): SendMode | undefined {
  if (metaKey || ctrlKey) return sendMode === "steer" ? "queue" : "steer";
  if (altKey) return sendMode === "add" ? "queue" : "add";
  return undefined;
}

/** The composer's hint for the one-off sends, e.g. "⌘Enter to steer, ⌥Enter to add". */
export function oneOffSendHint(sendMode: SendMode, isMac: boolean): string {
  const steer = oneOffSendMode(sendMode, { metaKey: true });
  const add = oneOffSendMode(sendMode, { altKey: true });
  return `${isMac ? "⌘" : "Ctrl+"}Enter to ${steer}, ${isMac ? "⌥" : "Alt+"}Enter to ${add}`;
}

/** The send mode a lone ⌘/Ctrl tap moves to: Queue → Add → Steer → Queue. */
const NEXT_SEND_MODE: Record<SendMode, SendMode> = { queue: "add", add: "steer", steer: "queue" };

/** The send mode after sendMode in the ⌘/Ctrl-tap cycle. */
export function nextSendMode(sendMode: SendMode): SendMode {
  return NEXT_SEND_MODE[sendMode];
}

/** A tap is a press and release of the modifier within this many ms. */
export const MODIFIER_TAP_MS = 500;

/** The key fields the tap detector reads from a keyboard event. */
export interface TapKey {
  key: string;
  repeat?: boolean;
}

/**
 * Detects a lone tap of the cycle modifier (Meta on macOS, Control
 * elsewhere): the modifier pressed and released quickly with no other key in
 * between. Any other key, a pointer press or a lost focus cancels it, so
 * ⌘C, ⌘-click and ⌘Tab never count as taps.
 */
export class ModifierTap {
  private downAt: number | null = null;

  constructor(
    private readonly modifier: "Meta" | "Control",
    private readonly now: () => number = Date.now,
  ) {}

  /** Records a keydown. */
  down(e: TapKey): void {
    if (e.key !== this.modifier) {
      this.downAt = null;
      return;
    }
    if (!e.repeat) this.downAt = this.now();
  }

  /** Records a keyup; true when it completes a tap. */
  up(e: TapKey): boolean {
    if (e.key !== this.modifier || this.downAt === null) return false;
    const tapped = this.now() - this.downAt <= MODIFIER_TAP_MS;
    this.downAt = null;
    return tapped;
  }

  /** Drops a pending press (pointer down, blur). */
  cancel(): void {
    this.downAt = null;
  }
}
