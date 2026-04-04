import { useCallback, useEffect, useRef, useState } from "react";
import type {
  AgentActivityData,
  AgentStatusData,
  AskUserQuestionData,
  ExitPlanModeData,
  MessageCreatedData,
  MessageStreamingData,
  TodoWriteData,
  ToolUseData,
  WSEvent,
} from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";

/** Ephemeral per-channel state that survives channel switches. */
export interface ActiveChatState {
  streamingContent: string | null;
  isRunning: boolean;
  runId: string | null;
  toolActivity: { tool_name: string; input: string } | null;
  agentActivity: AgentActivityData | null;
  askUserQuestions: AskUserQuestionData | null;
  exitPlanRequest: ExitPlanModeData | null;
  todos: TodoWriteData | null;
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
  const isRunningMapRef = useRef(new Map<string, string>());
  const unreadIdsRef = useRef(new Set<string>());
  const [unreadCount, setUnreadCount] = useState(0);

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
        map.set(ch.id, "");
      }
    }
  }, [channels]);

  // Compute subscription set: selectedId + all channels where isRunning.
  const subscribeChannels = useCallback(
    (send: (data: string) => void) => {
      const set = new Set<string>();
      if (selectedId) set.add(selectedId);
      for (const [id] of isRunningMapRef.current) {
        set.add(id);
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
        for (const [id] of isRunningMapRef.current) {
          set.add(id);
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

      // For agent.status events with thread_id, route state to the thread
      // so the parent channel doesn't show a running indicator for thread work.
      // The backend sends status to the parent with thread_id set; the frontend
      // uses thread_id as the effective target for store/isRunningMap updates.
      let stateTarget = channelId;
      if (channelId && wsEvent.type === "agent.status") {
        const statusData = wsEvent.data as AgentStatusData;
        if (statusData.thread_id) {
          stateTarget = statusData.thread_id;
        }
      }

      // Always update the store for any channel's events so getState()
      // returns current data even when a component remounts mid-stream.
      if (stateTarget) {
        const state = store.get(stateTarget);
        if (state) {
          applyEvent(state, wsEvent);
        } else if (isRunningEvent(wsEvent)) {
          const fresh = createEmptyState();
          applyEvent(fresh, wsEvent);
          store.set(stateTarget, fresh);
        }
      }

      // Track isRunning in the map for subscription management.
      // The map value is the run_id so we can distinguish concurrent runs.
      if (channelId && wsEvent.type === "agent.status") {
        const data = wsEvent.data as AgentStatusData;
        const runTarget = data.thread_id || channelId;
        if (data.status === "running") {
          runMap.set(runTarget, data.run_id ?? "");
        } else {
          // Clear the primary target (thread or channel).
          const finishing = data.run_id ?? "";
          {
            const tracked = runMap.get(runTarget);
            if (tracked === undefined || tracked === "" || finishing === "" || tracked === finishing) {
              runMap.delete(runTarget);
            }
          }
          // For thread-routed events, also clear the parent's entry if it
          // tracks the same run (bootstrapped from first-run running event
          // or seeded with "" by the channels-refresh effect on page reload).
          // Exact run_id match guards against clearing a concurrent user run;
          // empty-string match is safe because if the WS running event had
          // arrived it would have replaced "" with the real run_id already.
          if (runTarget !== channelId) {
            const parentTracked = runMap.get(channelId);
            if (parentTracked !== undefined && (parentTracked === "" || parentTracked === finishing)) {
              runMap.delete(channelId);
            }
          }
          // Keep the store entry — it holds completionInfo, mode, askUser, etc.
          // that should be restored when the user switches back.

          // Mark the task thread (or channel) as unread.
          const unreadTarget = data.thread_id || channelId;
          if (unreadTarget !== selectedIdRef.current || document.hidden) {
            unreadIdsRef.current.add(unreadTarget);
            setUnreadCount(unreadIdsRef.current.size);
          }
          if (document.hidden || unreadTarget !== selectedIdRef.current) {
            const ch = channelsRef.current.find((c) => c.id === unreadTarget) ?? channelsRef.current.find((c) => c.id === channelId);
            const name = ch?.name || channelId;
            const body =
              data.status === "completed"
                ? `Completed in ${Math.round((data.duration_ms ?? 0) / 1000)}s`
                : `Error: ${data.error ?? "unknown"}`;
            new Notification(`Loop — ${name}`, { body });
          }
        }
      }

      // Forward events to the chat listener (useChatState) when the
      // effective target matches the selected channel. Using stateTarget
      // (not channelId) ensures that agent.status events routed to a
      // thread via thread_id don't set isRunning on the parent view.
      if (stateTarget && stateTarget === selectedIdRef.current) {
        chatListenerRef.current?.(wsEvent);
      }

      // Forward selected channel + global events to App-level handler.
      if (!channelId || stateTarget === selectedIdRef.current) {
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
      for (const [id] of isRunningMapRef.current) {
        ids.push(id);
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
        isRunningMapRef.current.set(channelId, state.runId ?? "");
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
    setUnreadCount(unreadIdsRef.current.size);
  }, []);

  const markAllRead = useCallback(() => {
    unreadIdsRef.current.clear();
    setUnreadCount(0);
  }, []);

  return { getState, saveState, removeState, isRunningMapRef, unreadIdsRef, unreadCount, markRead, markAllRead, subscribeChatEvents };
}

// ── Helpers ──

function createEmptyState(): ActiveChatState {
  return {
    streamingContent: null,
    isRunning: false,
    runId: null,
    toolActivity: null,
    agentActivity: null,
    askUserQuestions: null,
    exitPlanRequest: null,
    todos: null,
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
    "agent.todos",
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
      if (data.tool_name === "EnterPlanMode") state.mode = "plan";
      if (data.tool_name === "ExitPlanMode") state.mode = "agent";
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
    case "agent.todos": {
      state.todos = event.data as TodoWriteData;
      break;
    }
    case "agent.status": {
      const data = event.data as AgentStatusData;
      if (data.status === "running") {
        state.isRunning = true;
        state.runId = data.run_id ?? null;
        state.completionInfo = null;
        state.askUserQuestions = null;
        state.exitPlanRequest = null;
        state.triggerContent = data.trigger_content ?? null;
      } else {
        // Only clear isRunning if the finishing run_id matches the one we're
        // tracking, or if either side has no run_id (backwards compat).
        const matchesRun = !state.runId || !data.run_id || state.runId === data.run_id;
        if (matchesRun) {
          state.isRunning = false;
          state.runId = null;
          state.toolActivity = null;
          state.agentActivity = null;
          state.triggerContent = null;
          // Clear todos only if all are completed; otherwise persist so the user sees remaining work.
          if (state.todos && state.todos.todos.every((t) => t.status === "completed")) {
            state.todos = null;
          }
        }
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
