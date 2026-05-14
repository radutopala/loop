import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentActivityData, AgentStatusData, AgentThinkingData, AskUserQuestionData, ExitPlanModeData, GateApprovalRequestedData, GateApprovalResolvedData, Message, MessageCreatedData, MessagesProcessedData, MessageStreamingData, TimelineItem, TodoWriteData, ToolResultData, ToolUseData, WSEvent } from "../types";
import type { ActiveChatState, ChatEventListener } from "./useChatStateStore";
import { useTimeline } from "./useTimeline";

export interface ChatState {
  items: TimelineItem[];
  liveTail: TimelineItem[];
  /** All messages from the timeline (kind=="message" only) — kept for callers
   * that still iterate by message identity (queued popup, processing label). */
  messages: Message[];
  loading: boolean;
  loadMore: () => void;
  hasMore: boolean;
  streamingContent: string | null;
  isRunning: boolean;
  toolActivity: { tool_name: string; input: string } | null;
  agentActivity: AgentActivityData | null;
  askUserQuestions: AskUserQuestionData | null;
  exitPlanRequest: ExitPlanModeData | null;
  todos: TodoWriteData | null;
  clearAskUser: () => void;
  clearExitPlan: () => void;
  mode: "agent" | "plan";
  setMode: (mode: "agent" | "plan") => void;
  completionInfo: { duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null;
  triggerContent: string | null;
  /**
   * Pending gate approval requests keyed by source. "chat" is the container
   * entrypoint chat agent; "terminal:<leafId>" is a specific terminal pane.
   * Each UI surface reads its own entry — both can be present at once.
   */
  gateApprovals: Record<string, GateApprovalRequestedData>;
  clearGateApproval: (source: string) => void;
  /** msg_id of the message the agent is currently processing — driven by agent.status backend event. */
  processingMsgId: string | null;
}

interface UseChatStateOptions {
  /** Restored state from the app-level store — used to initialize instead of defaults. */
  initialState?: ActiveChatState;
  /** Called on unmount with the latest state snapshot so the store can persist it. */
  onUnmount?: (state: ActiveChatState) => void;
  /**
   * Register to receive chat events from the app-level store's WebSocket.
   * When provided, useChatState does NOT open its own WebSocket connection —
   * events flow through the single store WS instead.
   */
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
}

/**
 * Manages chat state (messages, streaming, running status) and event stream.
 * Intended to be hoisted above layout switches so the WebSocket connection
 * and state persist when the user switches tabs.
 *
 * When `subscribeChatEvents` is provided (the normal case when used with the
 * app-level store), events arrive via the store's single WebSocket. Otherwise,
 * no events are received (the hook relies on initialState for restoration).
 */
export function useChatState(
  channelId: string | null,
  initialRunningBot?: boolean,
  options?: UseChatStateOptions,
): ChatState {
  const { initialState, onUnmount, subscribeChatEvents } = options ?? {};

  const {
    items,
    liveTail,
    loading,
    loadMore,
    hasMore,
    appendLiveMessage,
    appendLiveThinking,
    appendLiveToolUse,
    appendLiveToolResult,
    appendLiveCompacting,
    markProcessed,
    removeMessage,
    refetchHead,
  } = useTimeline(channelId);
  const [streamingContent, setStreamingContent] = useState<string | null>(initialState?.streamingContent ?? null);
  const [isRunning, setIsRunning] = useState(initialState?.isRunning ?? initialRunningBot ?? false);
  const [runId, setRunId] = useState<string | null>(initialState?.runId ?? null);
  const [toolActivity, setToolActivity] = useState<{ tool_name: string; input: string } | null>(initialState?.toolActivity ?? null);
  const [agentActivity, setAgentActivity] = useState<AgentActivityData | null>(initialState?.agentActivity ?? null);
  const [askUserQuestions, setAskUserQuestions] = useState<AskUserQuestionData | null>(initialState?.askUserQuestions ?? null);
  const [exitPlanRequest, setExitPlanRequest] = useState<ExitPlanModeData | null>(initialState?.exitPlanRequest ?? null);
  const [todos, setTodos] = useState<TodoWriteData | null>(initialState?.todos ?? null);
  const [mode, setMode] = useState<"agent" | "plan">(initialState?.mode ?? "agent");
  const [completionInfo, setCompletionInfo] = useState<{ duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null>(initialState?.completionInfo ?? null);
  const [triggerContent, setTriggerContent] = useState<string | null>(initialState?.triggerContent ?? null);
  const [gateApprovals, setGateApprovals] = useState<Record<string, GateApprovalRequestedData>>(initialState?.gateApprovals ?? {});
  const [processingMsgId, setProcessingMsgId] = useState<string | null>(initialState?.processingMsgId ?? null);

  // Refs tracking latest values for the onUnmount snapshot.
  const streamingRef = useRef(streamingContent);
  streamingRef.current = streamingContent;
  const isRunningRef = useRef(isRunning);
  isRunningRef.current = isRunning;
  const runIdRef = useRef(runId);
  runIdRef.current = runId;
  const toolRef = useRef(toolActivity);
  toolRef.current = toolActivity;
  const agentRef = useRef(agentActivity);
  agentRef.current = agentActivity;
  const askRef = useRef(askUserQuestions);
  askRef.current = askUserQuestions;
  const exitRef = useRef(exitPlanRequest);
  exitRef.current = exitPlanRequest;
  const todosRef = useRef(todos);
  todosRef.current = todos;
  const modeRef = useRef(mode);
  modeRef.current = mode;
  const completionRef = useRef(completionInfo);
  completionRef.current = completionInfo;
  const triggerRef = useRef(triggerContent);
  triggerRef.current = triggerContent;
  const gateApprovalsRef = useRef(gateApprovals);
  gateApprovalsRef.current = gateApprovals;
  const processingMsgIdRef = useRef(processingMsgId);
  processingMsgIdRef.current = processingMsgId;

  const onUnmountRef = useRef(onUnmount);
  onUnmountRef.current = onUnmount;

  // Call onUnmount on cleanup with the latest state snapshot.
  useEffect(() => {
    return () => {
      const snapshot = {
        streamingContent: streamingRef.current,
        isRunning: isRunningRef.current,
        runId: runIdRef.current,
        toolActivity: toolRef.current,
        agentActivity: agentRef.current,
        askUserQuestions: askRef.current,
        exitPlanRequest: exitRef.current,
        todos: todosRef.current,
        mode: modeRef.current,
        completionInfo: completionRef.current,
        triggerContent: triggerRef.current,
        gateApprovals: gateApprovalsRef.current,
        processingMsgId: processingMsgIdRef.current,
      };
      onUnmountRef.current?.(snapshot);
    };
  }, [channelId]);

  const handleEvent = useCallback(
    (event: WSEvent) => {
      if (event.type === "message.streaming") {
        const data = event.data as MessageStreamingData;
        setStreamingContent(data.content);
        return;
      }
      if (event.type === "message.created") {
        const data = event.data as MessageCreatedData;
        if (data.is_bot) {
          setStreamingContent(null);
        }
        appendLiveMessage({
          id: event.timestamp,
          channel_id: event.channel_id,
          msg_id: data.msg_id,
          author_id: data.author_id,
          author_name: data.author_name,
          content: data.content,
          is_bot: data.is_bot,
          is_processed: data.is_processed,
          priority: data.priority,
          created_at: new Date(event.timestamp).toISOString(),
        });
        return;
      }
      if (event.type === "messages.processed") {
        const data = event.data as MessagesProcessedData;
        markProcessed(data.msg_ids);
        if (processingMsgIdRef.current && data.msg_ids.includes(processingMsgIdRef.current)) {
          setProcessingMsgId(null);
        }
        return;
      }
      if (event.type === "message.deleted") {
        const data = event.data as { msg_id: string };
        removeMessage(data.msg_id);
        return;
      }
      if (event.type === "tool.use") {
        const data = event.data as ToolUseData;
        setToolActivity({ tool_name: data.tool_name, input: data.input });
        appendLiveToolUse(data.tool_use_id, data.tool_name, data.input);
        if (data.tool_name === "EnterPlanMode") setMode("plan");
        if (data.tool_name === "ExitPlanMode") setMode("agent");
        return;
      }
      if (event.type === "agent.thinking") {
        const data = event.data as AgentThinkingData;
        appendLiveThinking(data.text);
        return;
      }
      if (event.type === "tool.result") {
        const data = event.data as ToolResultData;
        appendLiveToolResult(data.tool_use_id, data.output, data.is_error ?? false);
        return;
      }
      if (event.type === "agent.activity") {
        const data = event.data as AgentActivityData;
        if (data.activity === "compacting") {
          // Persist as a timeline item so it survives subsequent activity
          // events overwriting the rolling indicator slot, and so the run
          // summary can count it.
          appendLiveCompacting();
        }
        setAgentActivity(data);
        return;
      }
      if (event.type === "agent.ask_user") {
        const data = event.data as AskUserQuestionData;
        setAskUserQuestions(data);
        return;
      }
      if (event.type === "agent.exit_plan") {
        const data = event.data as ExitPlanModeData;
        setExitPlanRequest(data);
        return;
      }
      if (event.type === "agent.todos") {
        const data = event.data as TodoWriteData;
        setTodos(data);
        return;
      }
      if (event.type === "gate.approval_requested") {
        const data = event.data as GateApprovalRequestedData;
        // Trust the backend's attribution. Older proxies / non-Linux hosts may
        // omit source; treat that as the chat agent so something renders.
        const source = data.source && data.source !== "" ? data.source : "chat";
        setGateApprovals((prev) => ({ ...prev, [source]: data }));
        return;
      }
      if (event.type === "gate.approval_resolved") {
        const data = event.data as GateApprovalResolvedData;
        setGateApprovals((prev) => {
          const next: Record<string, GateApprovalRequestedData> = {};
          let removed = false;
          for (const [k, v] of Object.entries(prev)) {
            if (v.req_id === data.req_id) { removed = true; continue; }
            next[k] = v;
          }
          return removed ? next : prev;
        });
        return;
      }
      if (event.type === "agent.status") {
        const data = event.data as AgentStatusData;
        if (data.status === "running") {
          // Sync the ref alongside the setter so a follow-up event in the same
          // tick (e.g. gate.approval_requested) can read the latest isRunning
          // before React commits the new state.
          isRunningRef.current = true;
          setIsRunning(true);
          setRunId(data.run_id ?? null);
          setCompletionInfo(null);
          setAskUserQuestions(null);
          setExitPlanRequest(null);
          setTriggerContent(data.trigger_content ?? null);
          setProcessingMsgId(data.msg_id ?? null);
        } else {
          // Only clear isRunning if the finishing run_id matches the one we're
          // tracking, or if either side has no run_id (backwards compat).
          const matchesRun = !runIdRef.current || !data.run_id || runIdRef.current === data.run_id;
          if (matchesRun) {
            isRunningRef.current = false;
            setIsRunning(false);
            setRunId(null);
            setToolActivity(null);
            setAgentActivity(null);
            setTriggerContent(null);
            // Clear todos when the agent turn ends.
            setTodos(null);
            // Drop any stale CHAT-sourced gate approval — the backend's
            // pending entry for the chat run is gone, so a click would 404.
            // Terminal-pane gates have independent lifecycles and stay.
            setGateApprovals((prev) => {
              if (!prev["chat"]) return prev;
              const { ["chat"]: _removed, ...rest } = prev;
              return rest;
            });
            setProcessingMsgId(null);
            // Refetch the head — JSONL ingest now has chain_position values for
            // the run's rows, so the persisted timeline supersedes the live tail.
            refetchHead();
          }
          // Don't clear askUserQuestions/exitPlanRequest on stop — they persist
          // until the user submits answers or approves the plan.
          if (data.status === "completed" && (data.duration_ms || data.stop_reason)) {
            setCompletionInfo({
              duration_ms: data.duration_ms,
              num_turns: data.num_turns,
              stop_reason: data.stop_reason,
              model: data.model,
            });
          }
        }
        return;
      }
    },
    [appendLiveMessage, appendLiveThinking, appendLiveToolUse, appendLiveToolResult, appendLiveCompacting, refetchHead],
  );

  // Subscribe to chat events from the app-level store (single WS).
  const handleEventRef = useRef(handleEvent);
  handleEventRef.current = handleEvent;

  useEffect(() => {
    if (!subscribeChatEvents) return;
    const listener: ChatEventListener = (event) => handleEventRef.current(event);
    return subscribeChatEvents(listener);
  }, [subscribeChatEvents]);

  // Derive the flat Message[] view used by the queued-popup and processing-label
  // logic. Includes both the persisted timeline messages and any live-tail ones.
  const messages: Message[] = [];
  for (const it of items) if (it.kind === "message") messages.push(it.data);
  for (const it of liveTail) if (it.kind === "message") messages.push(it.data);

  return {
    items,
    liveTail,
    messages,
    loading,
    loadMore,
    hasMore,
    streamingContent,
    isRunning,
    toolActivity,
    agentActivity,
    askUserQuestions,
    exitPlanRequest,
    todos,
    clearAskUser: useCallback(() => setAskUserQuestions(null), []),
    clearExitPlan: useCallback(() => setExitPlanRequest(null), []),
    mode,
    setMode,
    completionInfo,
    triggerContent,
    gateApprovals,
    clearGateApproval: useCallback((source: string) => {
      setGateApprovals((prev) => {
        if (!prev[source]) return prev;
        const next = { ...prev };
        delete next[source];
        return next;
      });
    }, []),
    processingMsgId,
  };
}
