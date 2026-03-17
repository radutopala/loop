import { useCallback, useState } from "react";
import type { AgentActivityData, AgentStatusData, AskUserQuestionData, ExitPlanModeData, Message, MessageCreatedData, MessageStreamingData, ToolUseData, WSEvent } from "../types";
import { useMessages } from "./useMessages";
import { useEventStream } from "./useEventStream";

export interface ChatState {
  messages: Message[];
  loading: boolean;
  loadMore: () => void;
  hasMore: boolean;
  addMessage: (msg: Message) => void;
  streamingContent: string | null;
  isRunning: boolean;
  toolActivity: { tool_name: string; input: string } | null;
  agentActivity: AgentActivityData | null;
  askUserQuestions: AskUserQuestionData | null;
  exitPlanRequest: ExitPlanModeData | null;
  clearAskUser: () => void;
  clearExitPlan: () => void;
  mode: "agent" | "plan";
  setMode: (mode: "agent" | "plan") => void;
  env: "local" | "worktree";
  setEnv: (env: "local" | "worktree") => void;
  selectedBranch: string | null;
  setSelectedBranch: (branch: string | null) => void;
  completionInfo: { duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null;
}

/**
 * Manages chat state (messages, streaming, running status) and event stream.
 * Intended to be hoisted above layout switches so the WebSocket connection
 * and state persist when the user switches tabs.
 */
export function useChatState(channelId: string | null, initialRunningBot?: boolean): ChatState {
  const { messages, loading, loadMore, hasMore, addMessage } = useMessages(channelId);
  const [streamingContent, setStreamingContent] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(initialRunningBot ?? false);
  const [toolActivity, setToolActivity] = useState<{ tool_name: string; input: string } | null>(null);
  const [agentActivity, setAgentActivity] = useState<AgentActivityData | null>(null);
  const [askUserQuestions, setAskUserQuestions] = useState<AskUserQuestionData | null>(null);
  const [exitPlanRequest, setExitPlanRequest] = useState<ExitPlanModeData | null>(null);
  const [mode, setMode] = useState<"agent" | "plan">("agent");
  const [env, setEnv] = useState<"local" | "worktree">("local");
  const [selectedBranch, setSelectedBranch] = useState<string | null>(null);
  const [completionInfo, setCompletionInfo] = useState<{ duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null>(null);

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
        addMessage({
          id: event.timestamp,
          channel_id: event.channel_id,
          msg_id: data.msg_id,
          author_id: data.author_id,
          author_name: data.author_name,
          content: data.content,
          is_bot: data.is_bot,
          created_at: new Date(event.timestamp).toISOString(),
        });
        return;
      }
      if (event.type === "tool.use") {
        const data = event.data as ToolUseData;
        setToolActivity({ tool_name: data.tool_name, input: data.input });
        return;
      }
      if (event.type === "agent.activity") {
        const data = event.data as AgentActivityData;
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
      if (event.type === "agent.status") {
        const data = event.data as AgentStatusData;
        if (data.status === "running") {
          setIsRunning(true);
          setCompletionInfo(null);
          setAskUserQuestions(null);
          setExitPlanRequest(null);
        } else {
          setIsRunning(false);
          setToolActivity(null);
          setAgentActivity(null);
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
    [addMessage],
  );

  useEventStream({ channelId, onEvent: handleEvent });

  return {
    messages,
    loading,
    loadMore,
    hasMore,
    addMessage,
    streamingContent,
    isRunning,
    toolActivity,
    agentActivity,
    askUserQuestions,
    exitPlanRequest,
    clearAskUser: useCallback(() => setAskUserQuestions(null), []),
    clearExitPlan: useCallback(() => setExitPlanRequest(null), []),
    mode,
    setMode,
    env,
    setEnv,
    selectedBranch,
    setSelectedBranch,
    completionInfo,
  };
}
