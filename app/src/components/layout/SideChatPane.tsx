import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RootEntry } from "../../api/files";
import type { SideChatMode } from "../../api/sideChats";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import { useContainerStats } from "../../hooks/useContainerStats";
import { ExplainContext, useExplain } from "../../hooks/useExplain";
import { LearnContext, useLearn } from "../../hooks/useLearn";
import { sideChats } from "../../layouts/sideChats";
import { useTheme } from "../../ThemeContext";
import { useChatPaneInput } from "../../uiBridge/useChatPaneInput";
import { ChatView } from "../chat/ChatView";
import { ExplainBadge } from "../chat/ExplainBadge";
import { explanationPending } from "../chat/explainState";
import { LearnBadge, usePaneHeaderSlot } from "../chat/LearnBadge";
import { ShellBadge } from "../chat/ShellBadge";
import { ContainerStatsLabel } from "../panels/ContainerStatsLabel";
import { ChatShellPane } from "./ChatShellPane";
import { ChatSlot, createChatHost } from "./ChatSlot";
import { ExplainPane } from "./ExplainPane";
import { LearnPane } from "./LearnPane";
import type { SideView } from "./LearnSplit";

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
  /** Reports the side chat's Explain, Learn or Shell view as it changes, and null
   *  once the pane goes, for the layout to show it. */
  onSideView?: (leafId: string, view: SideChatView | null) => void;
  /** Where the layout shows the view now (over the layout, or docked to the
   *  pane's tile on a canvas), or null while it's down. */
  sideViewPlace?: SideViewPlace;
}

/**
 * A side chat's Explain, Learn or Shell view, shown by the layout like the chat's:
 * split with the side chat over the layout, or docked to its tile on a
 * canvas. The side chat's chat and the pane beside it are mounted into
 * chatHost and paneHost, for the view to hold.
 */
export interface SideChatView {
  /** The view is open; view is kept while it closes. */
  open: boolean;
  view: SideView;
  /** Its explanation or learn pass is running. */
  running: boolean;
  openMode: SideChatMode;
  chatHost: HTMLElement;
  paneHost: HTMLElement;
  close: () => void;
}

export type SideViewPlace = "overlay" | "dock" | null;

/** The slot in the side chat view's chat header, for its badges. */
export function sideChatViewSlotId(leafId: string): string {
  return `side-chat-view-${leafId}`;
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
  return <SideChat key={sideChannelId} channelId={channelId} leafId={leafId} mode={mode} sideChannelId={sideChannelId} {...chat} />;
}

function SideChat({
  channelId,
  leafId,
  mode,
  sideChannelId,
  roots,
  onSelectThread,
  subscribeChannelEvents,
  getChatState,
  onChatStateUnmount,
  wsOpens,
  onSideView,
  sideViewPlace,
}: SideChatPaneProps & { sideChannelId: string }) {
  // The side chat isn't the selected channel, so its events come from its
  // own subscription, as the Learn view's thread does.
  const subscribe = useCallback((listener: ChatEventListener) => subscribeChannelEvents?.(sideChannelId, listener) ?? (() => {}), [sideChannelId, subscribeChannelEvents]);
  const onUnmount = useCallback((state: ActiveChatState) => onChatStateUnmount?.(sideChannelId, state), [sideChannelId, onChatStateUnmount]);
  const initialState = getChatState?.(sideChannelId);
  const chatState = useChatState(sideChannelId, initialState?.isRunning ?? false, { initialState, onUnmount, subscribeChatEvents: subscribe });
  useChatPaneInput(channelId, leafId, sideChannelId, chatState);
  // The side chat's own explanations and learn passes: its turns' Explain
  // and Learn actions, and the badges in its pane's header, open them in the
  // view the layout shows for the chat's (see SideChatView), with the side
  // chat in it. The Shell badge opens a shell in the side chat's container
  // there. The view (Explain, Learn or Shell) is kept while it closes.
  const [sideOpen, setSideOpen] = useState(false);
  const [sideView, setSideView] = useState<SideView>("explain");
  const show = useCallback((view: SideView) => {
    setSideView(view);
    setSideOpen(true);
  }, []);
  const showExplain = useCallback(() => show("explain"), [show]);
  const showLearn = useCallback(() => show("learn"), [show]);
  const showShell = useCallback(() => show("shell"), [show]);
  // The chat and the pane beside it are each mounted once, into a host the
  // pane or the view holds (see ChatSlot).
  const [chatHost] = useState(createChatHost);
  const [paneHost] = useState(createChatHost);
  const [chatPlaced, setChatPlaced] = useState(false);
  const placeChat = useCallback(() => setChatPlaced(true), []);
  useEffect(
    () => () => {
      for (const host of [chatHost, paneHost]) {
        host.closest("[data-chat-park]")?.remove();
        host.remove();
      }
    },
    [chatHost, paneHost],
  );
  // Closing it with focus in its pane would leave focus nowhere once the
  // pane goes: it goes back to the side chat's composer.
  const close = useCallback(() => {
    const focused = document.activeElement;
    setSideOpen(false);
    if (focused && paneHost.contains(focused)) chatHost.querySelector<HTMLTextAreaElement>("textarea")?.focus({ preventScroll: true });
  }, [chatHost, paneHost]);
  const explain = useExplain(sideChannelId, subscribeChannelEvents ? subscribe : undefined, wsOpens, showExplain);
  const learn = useLearn(sideChannelId, subscribeChannelEvents ? subscribe : undefined, subscribeChannelEvents, wsOpens, showLearn);
  const running = sideView === "explain" ? explain.explanations.some(explanationPending) : sideView === "learn" && learn.running;
  useEffect(() => {
    onSideView?.(leafId, { open: sideOpen, view: sideView, running, openMode: mode, chatHost, paneHost, close });
  }, [onSideView, leafId, sideOpen, sideView, running, mode, chatHost, paneHost, close]);
  useEffect(() => () => onSideView?.(leafId, null), [onSideView, leafId]);
  const overlaid = sideViewPlace === "overlay";
  const viewOpen = sideOpen && sideViewPlace != null;
  const toggleExplain = viewOpen && sideView === "explain" ? close : showExplain;
  const toggleLearn = viewOpen && sideView === "learn" ? close : showLearn;
  const toggleShell = viewOpen && sideView === "shell" ? close : showShell;
  // The side chat runs in a container of its own channel's, not the
  // channel's the pane header polls.
  const stats = useContainerStats(sideChannelId);
  const chatRunning = !!stats.agent;
  const slot = usePaneHeaderSlot(leafId);
  const viewSlotId = sideChatViewSlotId(leafId);
  const viewSlot = usePaneHeaderSlot(viewSlotId);
  return (
    <LearnContext.Provider value={learn}>
      <ExplainContext.Provider value={explain}>
        {slot && createPortal(<ContainerStatsLabel entry={stats.agent} />, slot)}
        {overlaid && viewSlot && createPortal(<ContainerStatsLabel entry={stats.agent} />, viewSlot)}
        <ExplainBadge leafId={leafId} explain={explain} open={viewOpen && sideView === "explain"} onToggle={toggleExplain} />
        <LearnBadge leafId={leafId} learn={learn} open={viewOpen && sideView === "learn"} onToggle={toggleLearn} />
        <ShellBadge leafId={leafId} running={chatRunning} open={viewOpen && sideView === "shell"} onToggle={toggleShell} />
        {overlaid && (
          <>
            <ExplainBadge leafId={viewSlotId} explain={explain} open={sideView === "explain"} onToggle={toggleExplain} />
            <LearnBadge leafId={viewSlotId} learn={learn} open={sideView === "learn"} onToggle={toggleLearn} />
            <ShellBadge leafId={viewSlotId} running={chatRunning} open={sideView === "shell"} onToggle={toggleShell} />
          </>
        )}
        {/* While the view is up over the layout it holds the chat, and this
            pane stays empty. Docked on a canvas, the chat stays here. */}
        <div data-testid="side-chat" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <ChatSlot host={chatHost} active={!overlaid} onAttach={placeChat} leafId={leafId} />
        </div>
        {chatPlaced &&
          createPortal(
            <ChatView
              channelId={sideChannelId}
              chatState={chatState}
              roots={roots}
              noAutoFocus
              onSelectThread={onSelectThread}
              subscribeChatEvents={subscribeChannelEvents ? subscribe : undefined}
              wsOpens={wsOpens}
            />,
            chatHost,
          )}
        {sideViewPlace != null &&
          createPortal(
            sideView === "explain" ? (
              <ExplainPane channelId={sideChannelId} explain={explain} onClose={close} />
            ) : sideView === "shell" ? (
              <ChatShellPane channelId={sideChannelId} onClose={close} />
            ) : (
              <LearnPane
                learn={learn}
                worktree={false}
                roots={roots}
                subscribeChannelEvents={subscribeChannelEvents}
                getChatState={getChatState}
                onChatStateUnmount={onChatStateUnmount}
                onClose={close}
              />
            ),
            paneHost,
          )}
      </ExplainContext.Provider>
    </LearnContext.Provider>
  );
}
