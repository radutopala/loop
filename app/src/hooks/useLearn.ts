import { createContext, useCallback, useEffect, useMemo, useState } from "react";
import { applyLearnProposal, dismissLearnProposal, fetchLearnProposals, fetchLearnState, type LearnProposal, type LearnState, setLearn as putLearn } from "../api/learn";
import { mergeProposals } from "../components/chat/learnState";
import type { AgentStatusData, WSEvent } from "../types";
import { logErr } from "../utils/log";
import type { ChatEventListener } from "./useChatStateStore";

export interface LearnView {
  channelId: string;
  /** False until the channel's learn state has loaded. */
  loaded: boolean;
  /** False for Slack and Discord channels, which never learn. */
  available: boolean;
  /** The channel's own switch: "on", "off", or "" to follow defaultLearn. */
  learn: LearnState["learn"];
  defaultLearn: boolean;
  setLearn: (learn: LearnState["learn"]) => Promise<void>;
  /** The hidden learn thread, "" until the channel's first learn pass. */
  learnChannelId: string;
  running: boolean;
  proposals: LearnProposal[];
  apply: (id: number) => Promise<void>;
  dismiss: (id: number) => Promise<void>;
}

/** The selected channel's LearnView, for the chat's Learn switch and badge. */
export const LearnContext = createContext<LearnView | null>(null);

/** The Learn drawer over the layout: whether it's open, and its toggle. */
export interface LearnDrawerControl {
  open: boolean;
  toggle: () => void;
}

export const LearnDrawerContext = createContext<LearnDrawerControl | null>(null);

/**
 * Follows a channel's learn pass: its switch, its hidden learn thread,
 * whether a pass is running there, and the proposals it filed. The learn
 * thread is never selected, so its run status comes through
 * subscribeChannelEvents; the channel.learn and learn.* events are global and
 * carry the learning channel's id, so they reach the selected channel's
 * listeners (and every window's).
 */
export function useLearn(
  channelId: string,
  subscribeChatEvents?: (listener: ChatEventListener) => () => void,
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void,
): LearnView {
  const [loaded, setLoaded] = useState(false);
  const [available, setAvailable] = useState(false);
  const [learn, setLearnValue] = useState<LearnState["learn"]>("");
  const [defaultLearn, setDefaultLearn] = useState(false);
  const [learnChannelId, setLearnChannelId] = useState("");
  const [running, setRunning] = useState(false);
  const [proposals, setProposals] = useState<LearnProposal[]>([]);

  useEffect(() => {
    let cancelled = false;
    setLoaded(false);
    setLearnChannelId("");
    setRunning(false);
    setProposals([]);
    fetchLearnState(channelId)
      .then((st) => {
        if (cancelled) return;
        setAvailable(st.available);
        setLearnValue(st.learn);
        setDefaultLearn(st.default_learn);
        // A learn.started that arrived while this was in flight is newer:
        // don't let the fetched state undo it.
        setLearnChannelId((cur) => cur || st.learn_channel_id);
        setRunning((cur) => cur || st.running);
        setLoaded(true);
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
        case "channel.learn":
          setLearnValue((event.data as { learn: LearnState["learn"] }).learn);
          break;
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

  const setLearn = useCallback(
    async (next: LearnState["learn"]) => {
      const prev = learn;
      setLearnValue(next);
      try {
        await putLearn(channelId, next);
      } catch (e) {
        setLearnValue(prev);
        logErr("setting learn")(e);
      }
    },
    [channelId, learn],
  );

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

  // Memoized: the layout's chat pane re-renders when it changes.
  return useMemo(
    () => ({ channelId, loaded, available, learn, defaultLearn, setLearn, learnChannelId, running, proposals, apply, dismiss }),
    [channelId, loaded, available, learn, defaultLearn, setLearn, learnChannelId, running, proposals, apply, dismiss],
  );
}
