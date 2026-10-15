import { useCallback, useEffect, useState } from "react";
import type { RootEntry } from "../../api/files";
import type { SideChatMode } from "../../api/sideChats";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import { sideChats } from "../../layouts/sideChats";
import { useTheme } from "../../ThemeContext";
import { useChatPaneInput } from "../../uiBridge/useChatPaneInput";
import { ChatView } from "../chat/ChatView";

interface SideChatPaneProps {
  channelId: string;
  leafId: string;
  mode: SideChatMode;
  roots?: RootEntry[];
  onSelectThread?: (threadId: string) => void;
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
  wsOpens?: number;
}

/**
 * SideChatPane is a chat-side pane: a chat of its own beside the channel's,
 * in a side chat started for the pane the first time it opens (a fork of the
 * channel's session, or a fresh one). Closing the pane ends the side chat.
 */
export function SideChatPane({ channelId, leafId, mode, ...chat }: SideChatPaneProps) {
  const { colors } = useTheme();
  const [sideChannelId, setSideChannelId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    setSideChannelId(null);
    setError(null);
    sideChats.open(channelId, leafId, mode).then(
      (id) => live && setSideChannelId(id),
      (err: unknown) => live && setError(err instanceof Error ? err.message : String(err)),
    );
    return () => {
      live = false;
    };
  }, [channelId, leafId, mode]);

  if (error !== null) {
    return (
      <div data-testid="side-chat-error" style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 6, padding: 24, textAlign: "center" }}>
        <div style={{ fontSize: 13, color: colors.text }}>Couldn't start the side chat</div>
        <div style={{ fontSize: 12, color: colors.error }}>{error}</div>
      </div>
    );
  }
  if (!sideChannelId) {
    return (
      <div data-testid="side-chat-starting" style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", fontSize: 12, color: colors.textDim }}>
        Starting the side chat…
      </div>
    );
  }
  return <SideChat key={sideChannelId} channelId={channelId} leafId={leafId} sideChannelId={sideChannelId} {...chat} />;
}

function SideChat({
  channelId,
  leafId,
  sideChannelId,
  roots,
  onSelectThread,
  subscribeChannelEvents,
  getChatState,
  onChatStateUnmount,
  wsOpens,
}: Omit<SideChatPaneProps, "mode"> & { sideChannelId: string }) {
  // The side chat isn't the selected channel, so its events come from its
  // own subscription, as the Learn view's thread does.
  const subscribe = useCallback((listener: ChatEventListener) => subscribeChannelEvents?.(sideChannelId, listener) ?? (() => {}), [sideChannelId, subscribeChannelEvents]);
  const onUnmount = useCallback((state: ActiveChatState) => onChatStateUnmount?.(sideChannelId, state), [sideChannelId, onChatStateUnmount]);
  const initialState = getChatState?.(sideChannelId);
  const chatState = useChatState(sideChannelId, initialState?.isRunning ?? false, { initialState, onUnmount, subscribeChatEvents: subscribe });
  useChatPaneInput(channelId, leafId, sideChannelId, chatState);
  return (
    <ChatView
      channelId={sideChannelId}
      chatState={chatState}
      roots={roots}
      noAutoFocus
      onSelectThread={onSelectThread}
      subscribeChatEvents={subscribeChannelEvents ? subscribe : undefined}
      wsOpens={wsOpens}
    />
  );
}
