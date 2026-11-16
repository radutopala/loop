import { useEffect, useMemo, useState } from "react";
import { fetchPromptEditState, onPromptEditSettled } from "../api/promptEdit";
import type { AgentStatusData, WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { fetchWithRetry } from "./useLearn";

/** The channel's edits by voice, for the Edits view beside its chat. */
export interface PromptEditsView {
  /** The hidden prompt-edit thread, "" until the channel's first edit. */
  editChannelId: string;
  /** An edit is running in it. */
  running: boolean;
}

/** Whether an edit runs after an agent.status event in the prompt-edit thread. */
export function promptEditRunning(status: AgentStatusData["status"]): boolean {
  return status === "running";
}

/**
 * The channel's prompt-edit thread, and whether an edit runs in it. The
 * thread is looked up on mount, on each WS reconnect (wsOpens), and after an
 * edit of the channel's ends while it isn't known yet (the first edit makes
 * it); its run status comes through subscribeChannelEvents, which also keeps
 * the hidden thread subscribed so its chat stays current.
 */
export function usePromptEdits(channelId: string, subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void, wsOpens = 0): PromptEditsView {
  const [editChannelId, setEditChannelId] = useState("");
  const [running, setRunning] = useState(false);
  // Bumped to look the thread up again.
  const [lookups, setLookups] = useState(0);

  useEffect(() => {
    setEditChannelId("");
    setRunning(false);
  }, [channelId]);

  useEffect(
    () =>
      fetchWithRetry(
        () => fetchPromptEditState(channelId),
        "prompt edit state",
        (st) => setEditChannelId(st.edit_channel_id),
      ),
    [channelId, wsOpens, lookups],
  );

  useEffect(() => {
    if (editChannelId) return;
    return onPromptEditSettled((id) => {
      if (id === channelId) setLookups((n) => n + 1);
    });
  }, [channelId, editChannelId]);

  useEffect(() => {
    if (!editChannelId || !subscribeChannelEvents) return;
    return subscribeChannelEvents(editChannelId, (event: WSEvent) => {
      if (event.type === "agent.status") setRunning(promptEditRunning((event.data as AgentStatusData).status));
    });
  }, [editChannelId, subscribeChannelEvents]);

  return useMemo(() => ({ editChannelId, running }), [editChannelId, running]);
}
