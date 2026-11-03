import { useEffect, useState } from "react";
import { fetchPromptSuggestion } from "../../api/channels";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { AgentStatusData, WSEvent } from "../../types";
import { logErr } from "../../utils/log";

/** The agent.prompt_suggestion event the daemon sends after a turn with the
 *  next prompt the agent predicts the user will send. */
export const PROMPT_SUGGESTION_EVENT = "agent.prompt_suggestion";

/** suggestionEvent tells whether event sets or drops channelId's suggestion:
 *  a prompt_suggestion event for it, or a run starting in it. A thread's run
 *  status, told to its parent too, is the thread's. */
export function suggestionEvent(event: WSEvent, channelId: string): boolean {
  if (event.channel_id !== channelId) return false;
  if (event.type === PROMPT_SUGGESTION_EVENT) return true;
  if (event.type !== "agent.status") return false;
  const d = event.data as AgentStatusData;
  return d.status === "running" && (!d.thread_id || d.thread_id === channelId);
}

/** applySuggestionEvent returns channelId's suggestion after a WS event: a
 *  prompt_suggestion event sets it, and a run starting drops it, since it
 *  was a guess at what would start one. Other events, and other channels',
 *  leave it as it is. */
export function applySuggestionEvent(suggestion: string | null, event: WSEvent, channelId: string): string | null {
  if (!suggestionEvent(event, channelId)) return suggestion;
  if (event.type === PROMPT_SUGGESTION_EVENT) {
    return (event.data as { suggestion?: string }).suggestion?.trim() || suggestion;
  }
  return null;
}

/** usePromptSuggestion follows the channel's suggested next prompt: the one
 *  the daemon kept, fetched when the chat opens and on each WS (re)connect,
 *  since it may have come while another chat was open, then its events.
 *  There's none in a chat without events (a read-only or Learn thread). */
export function usePromptSuggestion(
  channelId: string | null,
  subscribeChatEvents?: (listener: ChatEventListener) => () => void,
  wsOpens?: number,
  fetchSuggestion: (channelId: string) => Promise<string> = fetchPromptSuggestion,
): string | null {
  const [suggestion, setSuggestion] = useState<string | null>(null);
  useEffect(() => {
    setSuggestion(null);
    if (!channelId || !subscribeChatEvents) return;
    // An event heard while the fetch is out is newer than what it returns.
    let heard = false;
    let open = true;
    const unsubscribe = subscribeChatEvents((event) => {
      if (suggestionEvent(event, channelId)) heard = true;
      setSuggestion((s) => applySuggestionEvent(s, event, channelId));
    });
    fetchSuggestion(channelId)
      .then((text) => {
        if (open && !heard && text) setSuggestion(text);
      })
      .catch(logErr("fetching the prompt suggestion"));
    return () => {
      open = false;
      unsubscribe();
    };
  }, [subscribeChatEvents, channelId, wsOpens, fetchSuggestion]);
  return suggestion;
}

/** composerPlaceholder is what the empty composer shows: the suggestion,
 *  with Tab to take it, or else its usual hint. */
export function composerPlaceholder(suggestion: string | null | undefined, hint: string): string {
  return suggestion ? `${suggestion}  ⇥ Tab` : hint;
}
