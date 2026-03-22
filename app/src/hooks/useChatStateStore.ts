import { useCallback, useEffect, useRef } from "react";
import type {
  AgentActivityData,
  AgentStatusData,
  AskUserQuestionData,
  ExitPlanModeData,
  MessageCreatedData,
  MessageStreamingData,
  ToolUseData,
  WSEvent,
} from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";

/** Ephemeral per-channel state that survives channel switches. */
export interface ActiveChatState {
  streamingContent: string | null;
  isRunning: boolean;
  toolActivity: { tool_name: string; input: string } | null;
  agentActivity: AgentActivityData | null;
  askUserQuestions: AskUserQuestionData | null;
  exitPlanRequest: ExitPlanModeData | null;
  mode: "agent" | "plan";
  completionInfo: {
    duration_ms?: number;
    num_turns?: number;
    stop_reason?: string;
    model?: string;
  } | null;
  triggerContent: string | null;
}

/** Callback type for chat event listeners registered by useChatState. */
export type ChatEventListener = (event: WSEvent) => void;

interface UseChatStateStoreOptions {
  /** All known channels (used to seed isRunning from channel.agent_running). */
  channels: { id: string; name: string; agent_running: boolean }[];
  /** Currently selected channel ID. */
  selectedId: string | null;
  /** Callback for app-level events on the selected channel (channel.created/deleted, diff refresh). */
  onAppEvent: (event: WSEvent) => void;
}

/**
 * App-level store that keeps per-channel chat state in memory and maintains a
 * single WebSocket connection subscribed to all "interesting" channels
 * (selected + any channel with a running agent).
 *
 * Events for the selected channel are forwarded to:
 * 1. `onAppEvent` — for App.tsx (channel.created/deleted, diff stats)
 * 2. The registered chat event listener — for useChatState React state updates
 *
 * Events for non-selected running channels silently update the store so state
 * is warm when switching.
 */
export function useChatStateStore({
  channels,
  selectedId,
  onAppEvent,
}: UseChatStateStoreOptions) {
  const storeRef = useRef(new Map<string, ActiveChatState>());
  const isRunningMapRef = useRef(new Map<string, boolean>());
  const unreadIdsRef = useRef(new Set<string>());

  // Chat event listener registered by useChatState for the selected channel.
  const chatListenerRef = useRef<ChatEventListener | null>(null);

  // Keep a ref to selectedId so the WS message handler always sees the latest.
  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;

  const onAppEventRef = useRef(onAppEvent);
  onAppEventRef.current = onAppEvent;

  const channelsRef = useRef(channels);
  channelsRef.current = channels;

  // Request notification permission on mount.
  useEffect(() => {
    if ("Notification" in window && Notification.permission === "default") {
      Notification.requestPermission();
    }
  }, []);

  // Seed isRunningMap from channel.agent_running on each channels refresh.
  useEffect(() => {
    const map = isRunningMapRef.current;
    for (const ch of channels) {
      if (ch.agent_running && !map.has(ch.id)) {
        map.set(ch.id, true);
      }
    }
  }, [channels]);

  // Compute subscription set: selectedId + all channels where isRunning.
  const subscribeChannels = useCallback(
    (send: (data: string) => void) => {
      const set = new Set<string>();
      if (selectedId) set.add(selectedId);
      for (const [id, running] of isRunningMapRef.current) {
        if (running) set.add(id);
      }
      send(JSON.stringify({ type: "subscribe", channels: [...set] }));
    },
    [selectedId],
  );

  // Send subscribe on open.
  const { send } = useWebSocketConnection({
    path: "/api/ws",
    enabled: true,
    onOpen: useCallback(
      (ws: WebSocket) => {
        const set = new Set<string>();
        if (selectedIdRef.current) set.add(selectedIdRef.current);
        for (const [id, running] of isRunningMapRef.current) {
          if (running) set.add(id);
        }
        ws.send(
          JSON.stringify({ type: "subscribe", channels: [...set] }),
        );
      },
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [],
    ),
    onMessage: useCallback((event: MessageEvent) => {
      let wsEvent: WSEvent;
      try {
        wsEvent = JSON.parse(event.data);
      } catch {
        return;
      }

      const channelId = wsEvent.channel_id;
      const store = storeRef.current;
      const runMap = isRunningMapRef.current;

      // Update store for non-selected channels silently.
      if (channelId && channelId !== selectedIdRef.current) {
        const state = store.get(channelId);
        if (state) {
          applyEvent(state, wsEvent);
        } else if (isRunningEvent(wsEvent)) {
          const fresh = createEmptyState();
          applyEvent(fresh, wsEvent);
          store.set(channelId, fresh);
        }
      }

      // Track isRunning in the map for subscription management.
      if (channelId && wsEvent.type === "agent.status") {
        const data = wsEvent.data as AgentStatusData;
        if (data.status === "running") {
          runMap.set(channelId, true);
        } else {
          runMap.delete(channelId);
          // Keep the store entry — it holds completionInfo, mode, askUser, etc.
          // that should be restored when the user switches back.

          // Mark channel as unread and send system notification.
          if (channelId !== selectedIdRef.current || document.hidden) {
            unreadIdsRef.current.add(channelId);
          }
          if (document.hidden || channelId !== selectedIdRef.current) {
            const ch = channelsRef.current.find((c) => c.id === channelId);
            const name = ch?.name || channelId;
            const body =
              data.status === "completed"
                ? `Completed in ${Math.round((data.duration_ms ?? 0) / 1000)}s`
                : `Error: ${data.error ?? "unknown"}`;
            new Notification(`Loop — ${name}`, { body });
          }
        }
      }

      // Forward selected channel events to the chat listener (useChatState).
      if (channelId && channelId === selectedIdRef.current) {
        chatListenerRef.current?.(wsEvent);
      }

      // Forward selected channel + global events to App-level handler.
      if (!channelId || channelId === selectedIdRef.current) {
        onAppEventRef.current(wsEvent);
      }
    }, []),
  });

  // Re-subscribe when selectedId or isRunningMap changes.
  const sendRef = useRef(send);
  sendRef.current = send;

  useEffect(() => {
    subscribeChannels(sendRef.current);
  }, [subscribeChannels]);

  // Re-subscribe when isRunningMap changes (agent starts/stops).
  const prevSubKeyRef = useRef("");
  useEffect(() => {
    const interval = setInterval(() => {
      const ids = [selectedId ?? ""];
      for (const [id, running] of isRunningMapRef.current) {
        if (running) ids.push(id);
      }
      ids.sort();
      const key = ids.join(",");
      if (key !== prevSubKeyRef.current) {
        prevSubKeyRef.current = key;
        subscribeChannels(sendRef.current);
      }
    }, 500);
    return () => clearInterval(interval);
  }, [selectedId, subscribeChannels]);

  /**
   * Read stored state for a channel. Non-destructive — the entry stays in the
   * store until overwritten by saveState on the next unmount. Safe to call
   * during render (React StrictMode double-renders).
   */
  const getState = useCallback(
    (channelId: string): ActiveChatState | undefined => {
      return storeRef.current.get(channelId);
    },
    [],
  );

  const saveState = useCallback(
    (channelId: string, state: ActiveChatState) => {
      storeRef.current.set(channelId, state);
      if (state.isRunning) {
        isRunningMapRef.current.set(channelId, true);
      }
    },
    [],
  );

  const removeState = useCallback((channelId: string) => {
    storeRef.current.delete(channelId);
  }, []);

  /**
   * Register a chat event listener. Called by useChatState to receive events
   * for the currently selected channel without opening a separate WebSocket.
   * Returns an unsubscribe function.
   */
  const subscribeChatEvents = useCallback(
    (listener: ChatEventListener): (() => void) => {
      chatListenerRef.current = listener;
      return () => {
        if (chatListenerRef.current === listener) {
          chatListenerRef.current = null;
        }
      };
    },
    [],
  );

  const markRead = useCallback((channelId: string) => {
    unreadIdsRef.current.delete(channelId);
  }, []);

  const markAllRead = useCallback(() => {
    unreadIdsRef.current.clear();
  }, []);

  return { getState, saveState, removeState, isRunningMapRef, unreadIdsRef, markRead, markAllRead, subscribeChatEvents };
}

// ── Helpers ──

function createEmptyState(): ActiveChatState {
  return {
    streamingContent: null,
    isRunning: false,
    toolActivity: null,
    agentActivity: null,
    askUserQuestions: null,
    exitPlanRequest: null,
    mode: "agent",
    completionInfo: null,
    triggerContent: null,
  };
}

function isRunningEvent(event: WSEvent): boolean {
  return [
    "message.streaming",
    "tool.use",
    "agent.activity",
    "agent.ask_user",
    "agent.exit_plan",
    "agent.status",
  ].includes(event.type);
}

/** Mutates `state` in place based on the event. */
function applyEvent(state: ActiveChatState, event: WSEvent): void {
  switch (event.type) {
    case "message.streaming": {
      const data = event.data as MessageStreamingData;
      state.streamingContent = data.content;
      break;
    }
    case "message.created": {
      const data = event.data as MessageCreatedData;
      if (data.is_bot) {
        state.streamingContent = null;
      }
      break;
    }
    case "tool.use": {
      const data = event.data as ToolUseData;
      state.toolActivity = { tool_name: data.tool_name, input: data.input };
      break;
    }
    case "agent.activity": {
      state.agentActivity = event.data as AgentActivityData;
      break;
    }
    case "agent.ask_user": {
      state.askUserQuestions = event.data as AskUserQuestionData;
      break;
    }
    case "agent.exit_plan": {
      state.exitPlanRequest = event.data as ExitPlanModeData;
      break;
    }
    case "agent.status": {
      const data = event.data as AgentStatusData;
      if (data.status === "running") {
        state.isRunning = true;
        state.completionInfo = null;
        state.askUserQuestions = null;
        state.exitPlanRequest = null;
        state.triggerContent = data.trigger_content ?? null;
      } else {
        state.isRunning = false;
        state.toolActivity = null;
        state.agentActivity = null;
        state.triggerContent = null;
        if (
          data.status === "completed" &&
          (data.duration_ms || data.stop_reason)
        ) {
          state.completionInfo = {
            duration_ms: data.duration_ms,
            num_turns: data.num_turns,
            stop_reason: data.stop_reason,
            model: data.model,
          };
        }
      }
      break;
    }
  }
}
