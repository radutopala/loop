import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RootEntry } from "../api/files";
import { fetchPluginAgent } from "../api/plugins";
import { ChatView } from "../components/chat/ChatView";
import { usePaneHeaderSlot } from "../components/chat/LearnBadge";
import { LoopInfinityIcon } from "../components/LoopInfinityIcon";
import { useChatState } from "../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../hooks/useChatStateStore";
import { useTheme } from "../ThemeContext";
import { fonts } from "../theme";
import type { AgentStatusData, WSEvent } from "../types";
import { logErr } from "../utils/log";

/** Subscribes a listener to a channel's events, as the app's store does. */
export type SubscribeChannelEvents = (channelId: string, listener: ChatEventListener) => () => void;

/** A plugin's agent runs in a channel: the hidden thread they run in, once
 *  the plugin starts one there, and whether one runs. */
export interface PluginAgentView {
  threadId: string | null;
  running: boolean;
  /** Feeds a plugin.run_started event of the pane's channel. */
  onEvent: (ev: WSEvent) => void;
}

/** The thread a plugin.run_started event says pluginId's run started in,
 *  or null when the event is about something else. */
export function startedRunThread(ev: WSEvent, pluginId: string): string | null {
  if (ev.type !== "plugin.run_started") return null;
  const data = ev.data as { plugin_id?: string; thread_id?: string } | undefined;
  if (data?.plugin_id !== pluginId || !data.thread_id) return null;
  return data.thread_id;
}

/** Tracks pluginId's agent runs for the pane in channelId: looked up on
 *  mount, then followed through its events. */
export function usePluginAgent(pluginId: string, channelId: string, subscribeChannelEvents?: SubscribeChannelEvents): PluginAgentView {
  const [threadId, setThreadId] = useState<string | null>(null);
  const [running, setRunning] = useState(false);

  useEffect(() => {
    setThreadId(null);
    setRunning(false);
    if (!pluginId) return;
    let cancelled = false;
    fetchPluginAgent(pluginId, channelId)
      .then((a) => {
        if (cancelled || !a.thread_id) return;
        // A run that started while this was in flight is newer.
        setThreadId((cur) => cur ?? a.thread_id ?? null);
        setRunning((cur) => cur || a.running);
      })
      .catch(logErr("looking up the plugin's agent"));
    return () => {
      cancelled = true;
    };
  }, [pluginId, channelId]);

  const onEvent = useCallback(
    (ev: WSEvent) => {
      const started = startedRunThread(ev, pluginId);
      if (!started) return;
      setThreadId(started);
      setRunning(true);
    },
    [pluginId],
  );

  // Only the plugin's runs run in its thread: its status is theirs.
  useEffect(() => {
    if (!threadId || !subscribeChannelEvents) return;
    return subscribeChannelEvents(threadId, (ev: WSEvent) => {
      if (ev.type === "agent.status") setRunning((ev.data as AgentStatusData).status === "running");
    });
  }, [threadId, subscribeChannelEvents]);

  return { threadId, running, onEvent };
}

/**
 * The plugin pane's agent label, at the right of its header, once the
 * plugin has run an agent in the channel: it opens the agent's chat beside
 * the panel, and closes it. Portals into the pane header like LearnBadge.
 */
export function PluginAgentBadge({ leafId, running, open, onToggle }: { leafId: string; running: boolean; open: boolean; onToggle: () => void }) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  if (!slot) return null;
  return createPortal(
    <button
      data-testid="plugin-agent-badge"
      onClick={onToggle}
      title={open ? "Close the plugin's agent chat" : "Open the plugin's agent chat"}
      aria-expanded={open}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 16,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        background: open ? colors.hoverBg : "transparent",
        border: `1px solid ${colors.border}`,
        color: colors.textDim,
        cursor: "pointer",
        padding: "0 6px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 8,
      }}
    >
      <span style={{ display: "flex", zoom: 0.6 }}>
        <LoopInfinityIcon color={running ? undefined : colors.textDim} animated={running} isDark={colors.isDark} />
      </span>
      agent
    </button>,
    slot,
  );
}

/** The plugin's hidden thread as a chat to watch: its runs are the plugin's
 *  own, so nobody writes in it. Its state lives in the app's store while it's
 *  closed, like the Learn view's thread. */
export function PluginAgentThread({
  threadId,
  running,
  roots,
  subscribeChannelEvents,
  getChatState,
  onChatStateUnmount,
}: {
  threadId: string;
  running: boolean;
  roots?: RootEntry[];
  subscribeChannelEvents?: SubscribeChannelEvents;
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
}) {
  const subscribe = useCallback((listener: ChatEventListener) => subscribeChannelEvents?.(threadId, listener) ?? (() => {}), [threadId, subscribeChannelEvents]);
  const onUnmount = useCallback((state: ActiveChatState) => onChatStateUnmount?.(threadId, state), [threadId, onChatStateUnmount]);
  const chatState = useChatState(threadId, running, { initialState: getChatState?.(threadId), onUnmount, subscribeChatEvents: subscribe });
  return <ChatView channelId={threadId} chatState={chatState} roots={roots} noAutoFocus readOnlyNote="The plugin's agent runs: only the plugin starts them." />;
}
