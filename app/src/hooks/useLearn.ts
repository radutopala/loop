import { useCallback, useEffect, useState } from "react";
import { applyLearnProposal, dismissLearnProposal, fetchLearnProposals, fetchLearnState, type LearnProposal } from "../api/learn";
import { mergeProposals } from "../components/chat/learnState";
import type { AgentStatusData, WSEvent } from "../types";
import { logErr } from "../utils/log";
import type { ChatEventListener } from "./useChatStateStore";

export interface LearnView {
  /** The hidden learn thread, "" until the channel's first learn pass. */
  learnChannelId: string;
  running: boolean;
  proposals: LearnProposal[];
  apply: (id: number) => Promise<void>;
  dismiss: (id: number) => Promise<void>;
}

/**
 * Follows a channel's learn pass: its hidden learn thread, whether a pass is
 * running there, and the proposals it filed. The learn thread is never
 * selected, so its run status comes through subscribeChannelEvents; the
 * learn.* events are global and carry the learning channel's id, so they
 * reach the selected channel's listeners.
 */
export function useLearn(
  channelId: string,
  subscribeChatEvents?: (listener: ChatEventListener) => () => void,
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void,
): LearnView {
  const [learnChannelId, setLearnChannelId] = useState("");
  const [running, setRunning] = useState(false);
  const [proposals, setProposals] = useState<LearnProposal[]>([]);

  useEffect(() => {
    let cancelled = false;
    setLearnChannelId("");
    setRunning(false);
    setProposals([]);
    fetchLearnState(channelId)
      .then((st) => {
        if (cancelled) return;
        setLearnChannelId(st.learn_channel_id);
        setRunning(st.running);
      })
      .catch(logErr("fetching learn state"));
    fetchLearnProposals(channelId)
      .then((list) => {
        if (!cancelled) setProposals((cur) => mergeProposals(cur, list));
      })
      .catch(logErr("fetching learn proposals"));
    return () => {
      cancelled = true;
    };
  }, [channelId]);

  useEffect(() => {
    if (!subscribeChatEvents) return;
    return subscribeChatEvents((event: WSEvent) => {
      if (event.channel_id !== channelId) return;
      switch (event.type) {
        case "learn.started":
          setLearnChannelId((event.data as { learn_channel_id: string }).learn_channel_id);
          setRunning(true);
          break;
        case "learn.proposals":
          setProposals((cur) => mergeProposals(cur, (event.data as { proposals: LearnProposal[] }).proposals));
          break;
        case "learn.proposal_updated":
          setProposals((cur) => mergeProposals(cur, [event.data as LearnProposal]));
          break;
        default:
      }
    });
  }, [channelId, subscribeChatEvents]);

  useEffect(() => {
    if (!learnChannelId || !subscribeChannelEvents) return;
    return subscribeChannelEvents(learnChannelId, (event: WSEvent) => {
      if (event.type === "agent.status") setRunning((event.data as AgentStatusData).status === "running");
    });
  }, [learnChannelId, subscribeChannelEvents]);

  const apply = useCallback(async (id: number) => {
    try {
      const p = await applyLearnProposal(id);
      setProposals((cur) => mergeProposals(cur, [p]));
    } catch (e) {
      logErr("applying learn proposal")(e);
    }
  }, []);

  const dismiss = useCallback(async (id: number) => {
    try {
      const p = await dismissLearnProposal(id);
      setProposals((cur) => mergeProposals(cur, [p]));
    } catch (e) {
      logErr("dismissing learn proposal")(e);
    }
  }, []);

  return { learnChannelId, running, proposals, apply, dismiss };
}
