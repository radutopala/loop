import { useCallback, useEffect, useState } from "react";
import type { RootEntry } from "../../api/files";
import type { SideChatMode } from "../../api/sideChats";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import { ExplainContext, useExplain } from "../../hooks/useExplain";
import { LearnContext, useLearn } from "../../hooks/useLearn";
import { sideChats } from "../../layouts/sideChats";
import { SplitDivider } from "../../splitPane/SplitPaneLayout";
import { useTheme } from "../../ThemeContext";
import { useChatPaneInput } from "../../uiBridge/useChatPaneInput";
import { ChatView } from "../chat/ChatView";
import { ExplainBadge } from "../chat/ExplainBadge";
import { explanationPending } from "../chat/explainState";
import { LearnBadge } from "../chat/LearnBadge";
import { ExplainPane } from "./ExplainPane";
import { LearnPane } from "./LearnPane";
import { type SideView, SplitSeamLogo } from "./LearnSplit";

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
  // The side chat's own explanations and learn passes: its turns' Explain
  // and Learn actions, and the badges in its pane's header, open them in a
  // pane beside its chat, inside this pane.
  const [sideView, setSideView] = useState<SideView | null>(null);
  const showExplain = useCallback(() => setSideView("explain"), []);
  const showLearn = useCallback(() => setSideView("learn"), []);
  const closeSide = useCallback(() => setSideView(null), []);
  const explain = useExplain(sideChannelId, subscribeChannelEvents ? subscribe : undefined, wsOpens, showExplain);
  const learn = useLearn(sideChannelId, subscribeChannelEvents ? subscribe : undefined, subscribeChannelEvents, wsOpens, showLearn);
  const running = sideView === "explain" ? explain.explanations.some(explanationPending) : learn.running;
  return (
    <LearnContext.Provider value={learn}>
      <ExplainContext.Provider value={explain}>
        <ExplainBadge leafId={leafId} explain={explain} open={sideView === "explain"} onToggle={sideView === "explain" ? closeSide : showExplain} />
        <LearnBadge leafId={leafId} learn={learn} open={sideView === "learn"} onToggle={sideView === "learn" ? closeSide : showLearn} />
        <div data-testid="side-chat" data-view={sideView ?? ""} style={{ flex: 1, minHeight: 0, display: "flex", position: "relative" }}>
          <div style={{ flex: "1 1 0%", minWidth: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
            <ChatView
              channelId={sideChannelId}
              chatState={chatState}
              roots={roots}
              noAutoFocus
              onSelectThread={onSelectThread}
              subscribeChatEvents={subscribeChannelEvents ? subscribe : undefined}
              wsOpens={wsOpens}
            />
          </div>
          {sideView && (
            <>
              <SplitDivider vertical={false} />
              <div style={{ flex: "1 1 0%", minWidth: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
                {sideView === "explain" ? (
                  <ExplainPane channelId={sideChannelId} explain={explain} onClose={closeSide} />
                ) : (
                  <LearnPane
                    learn={learn}
                    worktree={false}
                    roots={roots}
                    subscribeChannelEvents={subscribeChannelEvents}
                    getChatState={getChatState}
                    onChatStateUnmount={onChatStateUnmount}
                    onClose={closeSide}
                  />
                )}
              </div>
              <SplitSeamLogo testId="side-chat-logo" running={running} />
            </>
          )}
        </div>
      </ExplainContext.Provider>
    </LearnContext.Provider>
  );
}
