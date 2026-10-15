import { useEffect, useRef } from "react";
import { sendMessage } from "../api/channels";
import type { ChatState } from "../hooks/useChatState";
import { registerChatInput } from "./registry";
import type { ChatPaneInput } from "./types";

/** chatPaneInput is what UI commands use of a chat: state() is its latest
 *  state, and its messages go to chatChannelId. */
export function chatPaneInput(chatChannelId: string, state: () => ChatState, now: () => number = Date.now): ChatPaneInput {
  return {
    // A message delayed to later doesn't keep the chat busy until then.
    busy: () => state().isRunning || state().queuedMessages.some((m) => !m.not_before || m.not_before * 1000 <= now()),
    blocked: () => {
      const s = state();
      if (s.askUserQuestions) return "a question";
      if (s.exitPlanRequest) return "a plan";
      return s.gateApprovals.chat ? "an approval" : null;
    },
    messages: () => state().messages.map((m) => ({ id: m.id, author: m.author_name, bot: m.is_bot, content: m.content })),
    send: (text) => sendMessage(chatChannelId, text, state().mode),
  };
}

/** Registers the chat of the chat or chat-side pane paneId of channelId's
 *  workspace, for UI commands; chatChannelId is the chat's channel, the
 *  workspace's or a side chat's. */
export function useChatPaneInput(channelId: string | null, paneId: string | undefined, chatChannelId: string | null, chatState: ChatState): void {
  const stateRef = useRef(chatState);
  stateRef.current = chatState;
  useEffect(() => {
    if (!channelId || !paneId || !chatChannelId) return;
    return registerChatInput(
      channelId,
      paneId,
      chatPaneInput(chatChannelId, () => stateRef.current),
    );
  }, [channelId, paneId, chatChannelId]);
}
