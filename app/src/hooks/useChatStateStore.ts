import { useCallback, useEffect, useRef, useState } from "react";
import { listPendingApprovals } from "../api/gate";
import { listReviewSessions } from "../api/review";
import type {
  AgentActivityData,
  AgentStatusData,
  AskUserQuestionData,
  ExitPlanModeData,
  GateApprovalRequestedData,
  GateApprovalResolvedData,
  MessageCreatedData,
  MessageStreamingData,
  MessagesProcessedData,
  AgentTasksData,
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
  agentTasks: AgentTasksData | null;
  mode: "agent" | "plan";
  completionInfo: {
    duration_ms?: number;
    num_turns?: number;
    stop_reason?: string;
    model?: string;
  } | null;
  triggerContent: string | null;
  /**
   * Pending gate approval requests keyed by source ("chat" or
   * "terminal:<leafId>"). Each UI surface looks up its own entry, so chat
   * and one or more terminal panes can have concurrent cards visible. A
   * backend-attribution miss (empty source) is bucketed under "chat" so
   * something still renders.
   */
  gateApprovals: Record<string, GateApprovalRequestedData>;
  /** msg_id of the message the agent is currently processing — backend-driven via agent.status. */
  processingMsgId: string | null;
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
  const gateChannelIdsRef = useRef(new Set<string>());
  const reviewChannelIdsRef = useRef(new Set<string>());
  const [unreadCount, setUnreadCount] = useState(0);
  const [, setGateTick] = useState(0);
  const [, setReviewTick] = useState(0);

  // Reconcile the sidebar's gate-indicator set against a channel's current
  // gateApprovals. Called after every gate/agent.status apply so the pill
  // appears as soon as a request lands and disappears once all sources resolve.
  const refreshGateMembership = useCallback((channelId: string, state: ActiveChatState) => {
    const set = gateChannelIdsRef.current;
    const has = set.has(channelId);
    const shouldHave = Object.keys(state.gateApprovals).length > 0;
    if (shouldHave && !has) {
      set.add(channelId);
      setGateTick((v) => v + 1);
    } else if (!shouldHave && has) {
      set.delete(channelId);
      setGateTick((v) => v + 1);
    }
  }, []);

  // Chat event listeners registered by panels (useChatState, useEditorState, …)
  // for the selected channel. A Set so multiple subscribers can coexist.
  const chatListenersRef = useRef<Set<ChatEventListener>>(new Set());

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

  // Pull the gate's current pending-approval list and reconcile both the
  // renderer's gateApprovals map and the electron-main dock-bouncer set
  // against it. Called from the WS onOpen callback so a reconnect (post
  // daemon restart, post network blip, post page reload) doesn't leave
  // either side desynced from the agentgate Managers.
  //
  // - Locally-known approvals with no matching server-side pending entry
  //   are dropped (treated as a deny event with actor="rehydrate").
  // - Server-side pendings we hadn't recorded yet are added.
  // - The full valid req_id set is then handed to electron-main so any
  //   bouncer entry that no longer corresponds to a live approval gets
  //   cleared — this is the actual fix for orphaned dock-bounce loops.
  const rehydrateGateApprovals = useCallback(async () => {
    let approvals;
    try {
      approvals = await listPendingApprovals();
    } catch (err) {
      console.warn("[rehydrate] listPendingApprovals failed:", err);
      return;
    }
    const valid = new Set<string>(approvals.map((a) => a.req_id));

    const now = Date.now();
    // 1. Synthesize a resolved event for any local entry missing from snapshot.
    for (const [channelId, state] of storeRef.current) {
      for (const entry of Object.values(state.gateApprovals)) {
        if (!valid.has(entry.req_id)) {
          const event: WSEvent = {
            type: "gate.approval_resolved",
            channel_id: channelId,
            data: { req_id: entry.req_id, decision: "deny", actor: "rehydrate" },
            timestamp: now,
          };
          applyEvent(state, event);
          if (channelId === selectedIdRef.current) {
            for (const listener of chatListenersRef.current) listener(event);
          }
        }
      }
    }

    // 2. Add any server-side pendings we didn't have.
    for (const pa of approvals) {
      let state = storeRef.current.get(pa.channel_id);
      if (!state) {
        state = createEmptyState();
        storeRef.current.set(pa.channel_id, state);
      }
      const already = Object.values(state.gateApprovals).some(
        (v) => v.req_id === pa.req_id,
      );
      if (already) continue;
      const event: WSEvent = {
        type: "gate.approval_requested",
        channel_id: pa.channel_id,
        data: pa,
        timestamp: now,
      };
      applyEvent(state, event);
      if (pa.channel_id === selectedIdRef.current) {
        for (const listener of chatListenersRef.current) listener(event);
      }
    }

    // 3. Reconcile the dock-bouncer set.
    window.loopAPI?.reconcileApprovals?.([...valid]);

    // 4. Reconcile the sidebar gate-indicator set against the post-rehydrate
    // gateApprovals so a stale pill from a since-resolved approval clears,
    // and a freshly-restored approval lights its channel up.
    for (const [id, state] of storeRef.current) {
      refreshGateMembership(id, state);
    }
  }, [refreshGateMembership]);

  // Pull the live (channel_id, status) snapshot of every review session
  // and reconcile reviewChannelIdsRef against it. Run from WS onOpen so
  // a renderer reload or WS reconnect re-lights the `rev` pill on every
  // channel whose session is still ready — review.status events only
  // fire on transitions, so without this any ready session that finished
  // while the app was closed would stay dark until the user reopened
  // the Review panel for that channel.
  const rehydrateReviewSessions = useCallback(async () => {
    let sessions;
    try {
      sessions = await listReviewSessions();
    } catch (err) {
      console.warn("[rehydrate] listReviewSessions failed:", err);
      return;
    }
    const ready = new Set<string>();
    for (const s of sessions) {
      if (s.status === "ready") ready.add(s.channel_id);
    }
    const set = reviewChannelIdsRef.current;
    let changed = false;
    for (const id of ready) {
      if (!set.has(id)) { set.add(id); changed = true; }
    }
    for (const id of [...set]) {
      if (!ready.has(id)) { set.delete(id); changed = true; }
    }
    if (changed) setReviewTick((v) => v + 1);
  }, []);

  // Also rehydrate when the user returns to Loop. The WS-onOpen path covers
  // reconnects and renderer reloads, but if a `gate.approval_requested`
  // arrived followed by a missed `gate.approval_resolved` on an otherwise-
  // healthy WS, the electron-main dock-bouncer set holds a stale req_id and
  // bounces forever. Reconciling against the gate snapshot whenever the
  // window regains focus or becomes visible self-heals that case without
  // waiting for a WS drop.
  //
  // Both events fire because they cover slightly different cases:
  // - `focus` fires on alt-tab back / click-into-window
  // - `visibilitychange` covers the renderer-not-active-surface case (e.g.
  //   DevTools focused or window occluded) where `focus` may not fire
  useEffect(() => {
    const rehydrate = () => { rehydrateGateApprovals(); };
    const onVisibility = () => { if (!document.hidden) rehydrate(); };
    window.addEventListener("focus", rehydrate);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("focus", rehydrate);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [rehydrateGateApprovals]);

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

  // Test hook (chromedp BDD): listen for a synthetic CustomEvent and route its
  // detail (a stringified WSEvent) through the same onMessage handler the real
  // WebSocket uses. Lets headless browser tests render plan/approval cards
  // without spinning up a real agent run.
  const onMessageRef = useRef<((event: MessageEvent) => void) | null>(null);
  useEffect(() => {
    const handler = (e: Event) => {
      const ce = e as CustomEvent<string>;
      onMessageRef.current?.({ data: ce.detail } as MessageEvent);
    };
    window.addEventListener("loop:test-event", handler);
    return () => window.removeEventListener("loop:test-event", handler);
  }, []);

  // Send subscribe on open.
  const handleMessage = useCallback((event: MessageEvent) => {
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
        let state = store.get(stateTarget);
        if (state) {
          applyEvent(state, wsEvent);
        } else if (isRunningEvent(wsEvent)) {
          const fresh = createEmptyState();
          applyEvent(fresh, wsEvent);
          store.set(stateTarget, fresh);
          state = fresh;
        }
        // gate.approval_requested / _resolved mutate gateApprovals directly;
        // agent.status non-running clears any "chat" approval (see applyEvent).
        // Each of those needs the sidebar set to follow along.
        if (
          state &&
          (wsEvent.type === "gate.approval_requested" ||
            wsEvent.type === "gate.approval_resolved" ||
            wsEvent.type === "agent.status")
        ) {
          refreshGateMembership(stateTarget, state);
        }
      }

      // Track which channels have a loaded review session so the sidebar
      // can render a `rev` pill alongside `gate`. Live events only — no
      // rehydration on reload, the pill reappears once the user opens the
      // Review panel for that channel and a fresh status event fires.
      if (channelId && wsEvent.type === "review.status") {
        const data = wsEvent.data as { status: string };
        const set = reviewChannelIdsRef.current;
        const has = set.has(channelId);
        const shouldHave = data.status === "ready";
        if (shouldHave && !has) {
          set.add(channelId);
          setReviewTick((v) => v + 1);
        } else if (!shouldHave && has) {
          set.delete(channelId);
          setReviewTick((v) => v + 1);
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
              // Also clear the parent's stored chat state and forward the
              // event to the parent's chat listener (if the parent is the
              // selected view). Without this, a first-run "running" event
              // broadcast to the parent (no thread existed yet) leaves the
              // parent's view stuck showing the stop button after the run
              // completes — the completion event carries thread_id and
              // gets routed exclusively to the thread's state.
              const parentState = store.get(channelId);
              if (parentState) {
                applyEvent(parentState, wsEvent);
              }
              if (channelId === selectedIdRef.current) {
                for (const listener of chatListenersRef.current) listener(wsEvent);
              }
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
          // Skip the dock bounce for non-user-driven runs — scheduled tasks
          // fire frequently and aren't user-actionable, and "bot" runs are
          // indirect chains (an agent re-entering via send_message /
          // create_thread MCP tools, often as part of a scheduled task).
          // Real user replies stay tagged as empty/user and still bounce.
          if (data.trigger !== "scheduled" && data.trigger !== "bot") {
            window.loopAPI?.notifyTurnEnd?.();
          }
        }
      }

      // Notify on a fresh approval request — the agent is blocked until the
      // user clicks Allow/Deny, so bounce the dock continuously and fire a
      // Web Notification when the relevant view isn't focused.
      if (channelId && wsEvent.type === "gate.approval_requested") {
        const reqData = wsEvent.data as GateApprovalRequestedData;
        const approvalTarget = stateTarget || channelId;
        if (document.hidden || approvalTarget !== selectedIdRef.current) {
          const ch = channelsRef.current.find((c) => c.id === approvalTarget) ?? channelsRef.current.find((c) => c.id === channelId);
          const name = ch?.name || channelId;
          const body = reqData.target ? `Approval needed: ${reqData.target}` : "Approval needed";
          new Notification(`Loop — ${name}`, { body });
        }
        console.log("[bounce] notifyApprovalNeeded reqId=%s channelId=%s target=%s", reqData.req_id, channelId, reqData.target);
        window.loopAPI?.notifyApprovalNeeded?.(reqData.req_id);
      }

      if (wsEvent.type === "gate.approval_resolved") {
        const data = wsEvent.data as GateApprovalResolvedData;
        console.log("[bounce] notifyApprovalResolved reqId=%s channelId=%s", data.req_id, channelId);
        window.loopAPI?.notifyApprovalResolved?.(data.req_id);
      }

      // Forward events to the chat listener (useChatState) when the
      // effective target matches the selected channel. Using stateTarget
      // (not channelId) ensures that agent.status events routed to a
      // thread via thread_id don't set isRunning on the parent view.
      if (stateTarget && stateTarget === selectedIdRef.current) {
        for (const listener of chatListenersRef.current) listener(wsEvent);
      }

      // Forward selected channel + global events to App-level handler.
      // Channel created/deleted are always forwarded so the sidebar refreshes
      // regardless of which channel is currently selected.
      if (!channelId || stateTarget === selectedIdRef.current || wsEvent.type === "channel.created" || wsEvent.type === "channel.deleted") {
        onAppEventRef.current(wsEvent);
      }
    }, []);
  onMessageRef.current = handleMessage;

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
        // After a (re)connect the renderer's in-memory gateApprovals and the
        // electron-main dock-bouncer set may both be stale: WS drops + page
        // reloads don't replay missed gate.approval_requested/_resolved
        // events. Snapshot the gate to (a) refill gateApprovals per channel
        // so cards reappear and (b) hand electron-main the canonical req_id
        // list so it can drop bouncer entries with no live request.
        rehydrateGateApprovals();
        rehydrateReviewSessions();
      },
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [],
    ),
    onMessage: handleMessage,
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
      chatListenersRef.current.add(listener);
      return () => {
        chatListenersRef.current.delete(listener);
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

  return { getState, saveState, removeState, isRunningMapRef, unreadIdsRef, gateChannelIdsRef, reviewChannelIdsRef, unreadCount, markRead, markAllRead, subscribeChatEvents };
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
    agentTasks: null,
    mode: "agent",
    completionInfo: null,
    triggerContent: null,
    gateApprovals: {},
    processingMsgId: null,
  };
}

function isRunningEvent(event: WSEvent): boolean {
  return [
    "message.streaming",
    "tool.use",
    "agent.activity",
    "agent.ask_user",
    "agent.exit_plan",
    "agent.tasks",
    "agent.status",
    "gate.approval_requested",
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
    case "agent.tasks": {
      state.agentTasks = event.data as AgentTasksData;
      break;
    }
    case "gate.approval_requested": {
      const data = event.data as GateApprovalRequestedData;
      // Trust the backend's attribution. Older proxies / non-Linux hosts may
      // omit source; treat that as the chat agent so something renders.
      const source = data.source && data.source !== "" ? data.source : "chat";
      state.gateApprovals = { ...state.gateApprovals, [source]: data };
      break;
    }
    case "gate.approval_resolved": {
      const data = event.data as GateApprovalResolvedData;
      const next: Record<string, GateApprovalRequestedData> = {};
      let removed = false;
      for (const [k, v] of Object.entries(state.gateApprovals)) {
        if (v.req_id === data.req_id) { removed = true; continue; }
        next[k] = v;
      }
      if (removed) state.gateApprovals = next;
      break;
    }
    case "messages.processed": {
      const data = event.data as MessagesProcessedData;
      if (state.processingMsgId && data.msg_ids.includes(state.processingMsgId)) {
        state.processingMsgId = null;
      }
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
        // Empty-string trigger_content (queue-drain, subagent) is no signal;
        // fall through to per-message content in the trigger-quote banner.
        state.triggerContent = data.trigger_content ? data.trigger_content : null;
        state.processingMsgId = data.msg_id ?? null;
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
          // Clear agent tasks when the agent turn ends.
          state.agentTasks = null;
          // Drop any stale chat-sourced gate approval so a remount doesn't
          // rehydrate it. Terminal-pane gates have their own lifecycle and
          // are NOT cleared by the chat agent's run ending.
          if (state.gateApprovals["chat"]) {
            const { ["chat"]: _removed, ...rest } = state.gateApprovals;
            state.gateApprovals = rest;
          }
          state.processingMsgId = null;
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
