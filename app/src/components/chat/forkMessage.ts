import type { Message } from "../../types";

/**
 * Whether a chat bubble offers "+fork": a bot reply that recorded where it
 * sits in Claude's transcript, or a user message its run has taken (not one
 * still queued or being processed — the conversation has no place for it yet).
 */
export function canForkMessage(message: Message, state: { queued?: boolean; processing?: boolean } = {}): boolean {
  if (message.is_bot) return !!message.forkable;
  return message.is_processed && !state.queued && !state.processing;
}
