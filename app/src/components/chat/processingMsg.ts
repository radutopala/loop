import type { Message } from "../../types";

/**
 * processingMsgIdOf picks the queued message the running agent is working on.
 * agent.status names it; until that arrives (e.g. after a remount), it's the
 * row the backend marked as claimed. A run with no claimed row, such as a
 * scheduled task writing to this thread, is working on none of them: the
 * messages behind it are all still waiting.
 */
export function processingMsgIdOf(processingMsgId: string | null, isRunning: boolean, backendQueue: Message[]): string | null {
  if (processingMsgId) return processingMsgId;
  if (!isRunning) return null;
  return backendQueue.find((m) => m.is_running)?.msg_id ?? null;
}
