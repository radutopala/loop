import { createContext, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { applyLearnProposal, dismissLearnProposal, fetchLearnProposals, fetchLearnState, type LearnProposal, type LearnState, setLearn as putLearn } from "../api/learn";
import { appliedShortcut, inBulk, isOpenProposal, type LearnBulk, learnPassRunning, mergeProposals, nextStaleIn } from "../components/chat/learnState";
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
  /** A learn pass is running (not a reply the user asked the learn thread for). */
  running: boolean;
  proposals: LearnProposal[];
  /** The proposals still waiting on the user (see isOpenProposal). One stuck
   * applying joins them as it goes stale. */
  open: LearnProposal[];
  /** Proposals with an apply or dismiss in flight. */
  busy: ReadonlySet<number>;
  /** A request that failed (the server never answered for the proposal), by
   * proposal, until the next try. A proposal the server failed to apply
   * comes back with status "failed" and its error instead. */
  errors: ReadonlyMap<number, string>;
  /** Apply all or Dismiss all is going through the proposals. */
  bulk: LearnBulk | null;
  apply: (id: number) => Promise<void>;
  dismiss: (id: number) => Promise<void>;
  applyAll: () => Promise<void>;
  dismissAll: () => Promise<void>;
  /** Bumped when an applied proposal added a prompt or bash shortcut. */
  shortcutsVersion: number;
}

// How long to wait before fetching again after a failure, doubling up to
// the cap.
const LEARN_RETRY_MS = 2_000;
const LEARN_RETRY_MAX_MS = 30_000;

/** The selected channel's LearnView, for the chat's Learn switch. */
export const LearnContext = createContext<LearnView | null>(null);

/**
 * Runs fetch until it succeeds, waiting after each failure (LEARN_RETRY_MS,
 * doubling up to LEARN_RETRY_MAX_MS), and hands the result to onLoad.
 * Returns a cancel function: after it, nothing is fetched or handed on.
 */
export function fetchWithRetry<T>(fetch: () => Promise<T>, what: string, onLoad: (v: T) => void): () => void {
  let cancelled = false;
  let retry: ReturnType<typeof setTimeout> | undefined;
  const load = (wait: number) => {
    fetch()
      .then((v) => {
        if (!cancelled) onLoad(v);
      })
      .catch((e) => {
        logErr(what)(e);
        if (!cancelled) retry = setTimeout(() => load(Math.min(wait * 2, LEARN_RETRY_MAX_MS)), wait);
      });
  };
  load(LEARN_RETRY_MS);
  return () => {
    cancelled = true;
    clearTimeout(retry);
  };
}

/**
 * Follows a channel's learn pass: its switch, its hidden learn thread,
 * whether a pass is running there, and the proposals it filed, with the
 * applies and dismisses in flight (kept here, not in the Learn pane, so they
 * outlive the Learn view closing). The learn thread is never selected, so
 * its run status comes through subscribeChannelEvents; the channel.learn and
 * learn.* events are global and carry the learning channel's id, so they
 * reach the selected channel's listeners (and every window's). Events missed
 * while the WS was down are made up for by fetching again on each
 * reconnect (wsOpens).
 */
export function useLearn(
  channelId: string,
  subscribeChatEvents?: (listener: ChatEventListener) => () => void,
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void,
  wsOpens = 0,
): LearnView {
  const [loaded, setLoaded] = useState(false);
  const [available, setAvailable] = useState(false);
  const [learn, setLearnValue] = useState<LearnState["learn"]>("");
  const [defaultLearn, setDefaultLearn] = useState(false);
  const [learnChannelId, setLearnChannelId] = useState("");
  const [running, setRunning] = useState(false);
  const [proposals, setProposals] = useState<LearnProposal[]>([]);
  const proposalsRef = useRef(proposals);
  proposalsRef.current = proposals;
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set());
  // Mirrors busy for Apply all and Dismiss all, which read it between one
  // request and the next.
  const busyRef = useRef(busy);
  const [errors, setErrors] = useState<ReadonlyMap<number, string>>(new Map());
  const [bulk, setBulk] = useState<LearnBulk | null>(null);
  // Bumped per Apply all or Dismiss all, and on a channel switch: a bulk run
  // stops once it's no longer the latest.
  const bulkRunRef = useRef(0);
  const [shortcutsVersion, setShortcutsVersion] = useState(0);
  const noteApplied = useCallback((list: LearnProposal[]) => {
    if (appliedShortcut(list)) setShortcutsVersion((n) => n + 1);
  }, []);

  useEffect(() => {
    setLoaded(false);
    setLearnChannelId("");
    setRunning(false);
    setProposals([]);
    setErrors(new Map());
    bulkRunRef.current++;
    setBulk(null);
    // Until the state loads the Learn switch stays hidden, so a failed
    // fetch is tried again rather than hiding it for good.
    const cancelState = fetchWithRetry(
      () => fetchLearnState(channelId),
      "fetching learn state",
      (st) => {
        setAvailable(st.available);
        setLearnValue(st.learn);
        setDefaultLearn(st.default_learn);
        // A learn.started that arrived while this was in flight is newer:
        // don't let the fetched state undo it.
        setLearnChannelId((cur) => cur || st.learn_channel_id);
        setRunning((cur) => cur || st.running);
        setLoaded(true);
      },
    );
    const cancelProposals = fetchWithRetry(
      () => fetchLearnProposals(channelId),
      "fetching learn proposals",
      (list) => setProposals((cur) => mergeProposals(cur, list)),
    );
    return () => {
      cancelState();
      cancelProposals();
    };
  }, [channelId]);

  // After a reconnect, the events missed meanwhile are lost: a pass that
  // ended would stay "learning…" and the proposals it filed would be
  // missing. What the server says now wins. The first open isn't a
  // reconnect: the fetches above cover it.
  const wsOpensRef = useRef(wsOpens);
  useEffect(() => {
    const first = wsOpensRef.current === 0;
    if (wsOpens === wsOpensRef.current) return;
    wsOpensRef.current = wsOpens;
    if (first) return;
    const cancelState = fetchWithRetry(
      () => fetchLearnState(channelId),
      "fetching learn state",
      (st) => {
        setAvailable(st.available);
        setLearnValue(st.learn);
        setDefaultLearn(st.default_learn);
        setLearnChannelId((cur) => st.learn_channel_id || cur);
        setRunning(st.running);
        setLoaded(true);
      },
    );
    const cancelProposals = fetchWithRetry(
      () => fetchLearnProposals(channelId),
      "fetching learn proposals",
      (list) => {
        setProposals((cur) => mergeProposals(cur, list));
        noteApplied(list);
      },
    );
    return () => {
      cancelState();
      cancelProposals();
    };
  }, [wsOpens, channelId, noteApplied]);

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
        case "learn.proposals": {
          const list = (event.data as { proposals: LearnProposal[] }).proposals;
          setProposals((cur) => mergeProposals(cur, list));
          noteApplied(list);
          break;
        }
        case "learn.proposal_updated": {
          const p = event.data as LearnProposal;
          setProposals((cur) => mergeProposals(cur, [p]));
          noteApplied([p]);
          break;
        }
        default:
      }
    });
  }, [channelId, subscribeChatEvents, noteApplied]);

  useEffect(() => {
    if (!learnChannelId || !subscribeChannelEvents) return;
    return subscribeChannelEvents(learnChannelId, (event: WSEvent) => {
      if (event.type !== "agent.status") return;
      const data = event.data as AgentStatusData;
      setRunning((cur) => learnPassRunning(cur, data.status, data.trigger));
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

  // An apply or dismiss: the proposal's buttons are disabled meanwhile, so a
  // second click can't race the first, and a failed request's error is kept
  // for the proposal until its next try.
  const settle = useCallback(
    async (id: number, action: (id: number) => Promise<LearnProposal>) => {
      busyRef.current = new Set(busyRef.current).add(id);
      setBusy(busyRef.current);
      setErrors((cur) => withError(cur, id, undefined));
      try {
        const p = await action(id);
        setProposals((cur) => mergeProposals(cur, [p]));
        noteApplied([p]);
      } catch (e) {
        setErrors((cur) => withError(cur, id, e instanceof Error ? e.message : String(e)));
      } finally {
        const next = new Set(busyRef.current);
        next.delete(id);
        busyRef.current = next;
        setBusy(next);
      }
    },
    [noteApplied],
  );
  const apply = useCallback((id: number) => settle(id, applyLearnProposal), [settle]);
  const dismiss = useCallback((id: number) => settle(id, dismissLearnProposal), [settle]);

  // One by one, each still in the bulk's scope when its turn comes: one
  // applied, dismissed or in flight meanwhile is left alone.
  const runBulk = useCallback(
    async (kind: LearnBulk) => {
      const run = ++bulkRunRef.current;
      setBulk(kind);
      for (const { id } of proposalsRef.current.filter((p) => inBulk(kind, p))) {
        if (bulkRunRef.current !== run) return;
        const p = proposalsRef.current.find((x) => x.id === id);
        if (inBulk(kind, p) && !busyRef.current.has(id)) await settle(id, kind === "apply" ? applyLearnProposal : dismissLearnProposal);
      }
      if (bulkRunRef.current === run) setBulk(null);
    },
    [settle],
  );
  const applyAll = useCallback(() => runBulk("apply"), [runBulk]);
  const dismissAll = useCallback(() => runBulk("dismiss"), [runBulk]);

  // A proposal stuck applying opens again once it's stale: render again
  // then, as nothing else may.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const due = nextStaleIn(proposals);
    if (due === null) return;
    const t = setTimeout(() => setTick((n) => n + 1), due + 1);
    return () => clearTimeout(t);
  }, [proposals, tick]);
  // tick: which proposals went stale is checked again.
  const open = useMemo(() => proposals.filter((p) => isOpenProposal(p)), [proposals, tick]);

  // Memoized: the layout's chat pane re-renders when it changes.
  return useMemo(
    () => ({
      channelId,
      loaded,
      available,
      learn,
      defaultLearn,
      setLearn,
      learnChannelId,
      running,
      proposals,
      open,
      busy,
      errors,
      bulk,
      apply,
      dismiss,
      applyAll,
      dismissAll,
      shortcutsVersion,
    }),
    [channelId, loaded, available, learn, defaultLearn, setLearn, learnChannelId, running, proposals, open, busy, errors, bulk, apply, dismiss, applyAll, dismissAll, shortcutsVersion],
  );
}

function withError(errors: ReadonlyMap<number, string>, id: number, error: string | undefined): ReadonlyMap<number, string> {
  if (errors.get(id) === error) return errors;
  const next = new Map(errors);
  if (error === undefined) next.delete(id);
  else next.set(id, error);
  return next;
}
