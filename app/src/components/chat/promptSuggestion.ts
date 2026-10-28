import { useEffect, useState } from "react";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { AgentStatusData, WSEvent } from "../../types";

/** The agent.prompt_suggestion event the daemon sends after a turn with the
 *  next prompt the agent predicts the user will send. */
export const PROMPT_SUGGESTION_EVENT = "agent.prompt_suggestion";

/** applySuggestionEvent returns channelId's suggestion after a WS event: a
 *  prompt_suggestion event sets it, and a run starting drops it, since it
 *  was a guess at what would start one. Other events, and other channels',
 *  leave it as it is. */
export function applySuggestionEvent(suggestion: string | null, event: WSEvent, channelId: string): string | null {
  if (event.channel_id !== channelId) return suggestion;
  if (event.type === PROMPT_SUGGESTION_EVENT) {
    return (event.data as { suggestion?: string }).suggestion?.trim() || suggestion;
  }
  if (event.type === "agent.status" && (event.data as AgentStatusData).status === "running") return null;
  return suggestion;
}

/** usePromptSuggestion follows the channel's suggested next prompt; there's
 *  none in a chat without events (a read-only or Learn thread). */
export function usePromptSuggestion(channelId: string | null, subscribeChatEvents?: (listener: ChatEventListener) => () => void): string | null {
  const [suggestion, setSuggestion] = useState<string | null>(null);
  useEffect(() => {
    setSuggestion(null);
    if (!channelId || !subscribeChatEvents) return;
    return subscribeChatEvents((event) => setSuggestion((s) => applySuggestionEvent(s, event, channelId)));
  }, [subscribeChatEvents, channelId]);
  return suggestion;
}

/** composerPlaceholder is what the empty composer shows: the suggestion,
 *  with Tab to take it, or else its usual hint. */
export function composerPlaceholder(suggestion: string | null | undefined, hint: string): string {
  return suggestion ? `${suggestion}  ⇥ Tab` : hint;
}
