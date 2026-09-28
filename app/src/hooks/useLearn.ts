import { createContext, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  applyLearnProposal,
  dismissLearnProposal,
  fetchLearnPasses,
  fetchLearnProposals,
  fetchLearnState,
  type LearnPass,
  type LearnProposal,
  type LearnState,
  learnTurn as postLearnTurn,
  setLearn as putLearn,
} from "../api/learn";
import {
  inBulk,
  isOpenProposal,
  type LearnBulk,
  learnPassesByMessage,
  learnPassRunning,
  learnProposalsEventItems,
  mergeLearnPasses,
  mergeProposals,
  newlyAppliedShortcut,
  nextStaleIn,
} from "../components/chat/learnState";
import type { AgentStatusData, WSEvent } from "../types";
import { logErr } from "../utils/log";
import type { ChatEventListener } from "./useChatStateStore";

export interface LearnView {
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
  /** The channel's learn passes, one per reviewed turn, newest first. */
  passes: LearnPass[];
  /** Each reviewed turn's newest pass, by the turn's last bot message. */
  passByMessage: ReadonlyMap<string, LearnPass>;
  /** The turn a turn's Learn action asked for, for the Learn pane to bring
   * its proposals into view; seq tells a second ask for the same turn. */
  focus: { messageId: string; seq: number } | null;
  /** Shows a turn's proposals in the Learn view. */
  show: (messageId: string) => void;
  /** Learns from a turn (a pass of its own, whatever the Learn switch
   * says), then shows it in the Learn view. */
  learnTurn: (messageId: string) => Promise<void>;
  /** Turns with a learnTurn request in flight. */
  turnBusy: ReadonlySet<string>;
  /** A learnTurn request that failed, by turn, until its next try. */
  turnErrors: ReadonlyMap<string, string>;
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
 * An apply or dismiss of one proposal: it's marked busy meanwhile (its
 * buttons disabled, so a second click can't race the first), its last
 * request error is cleared, and the settled proposal is handed on. A failed
 * request's error is kept for the proposal until its next try.
 */
export async function settleProposal(
  id: number,
  action: (id: number) => Promise<LearnProposal>,
  ops: {
    busy: { get: () => ReadonlySet<number>; set: (next: ReadonlySet<number>) => void };
    setError: (id: number, error: string | undefined) => void;
    settled: (p: LearnProposal) => void;
  },
): Promise<void> {
  ops.busy.set(new Set(ops.busy.get()).add(id));
  ops.setError(id, undefined);
  try {
    ops.settled(await action(id));
  } catch (e) {
    ops.setError(id, e instanceof Error ? e.message : String(e));
  } finally {
    const next = new Set(ops.busy.get());
    next.delete(id);
    ops.busy.set(next);
  }
}

/**
 * Apply all and Dismiss all: one by one, each proposal still in the bulk's
 * scope when its turn comes (one applied, dismissed, withdrawn or in flight
 * meanwhile is left alone). It stops once stopped (the hook unmounting).
 */
export async function runBulk(kind: LearnBulk, proposals: () => LearnProposal[], busy: () => ReadonlySet<number>, settle: (id: number) => Promise<void>, stopped: () => boolean): Promise<void> {
  for (const { id } of proposals().filter((p) => inBulk(kind, p))) {
    if (stopped()) return;
    if (!busy().has(id) && proposals().some((p) => p.id === id && inBulk(kind, p))) await settle(id);
  }
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
  onShow?: () => void,
): LearnView {
  const [loaded, setLoaded] = useState(false);
  const [available, setAvailable] = useState(false);
  const [learn, setLearnValue] = useState<LearnState["learn"]>("");
  const [defaultLearn, setDefaultLearn] = useState(false);
  const [learnChannelId, setLearnChannelId] = useState("");
  const [running, setRunning] = useState(false);
  const [proposals, setProposals] = useState<LearnProposal[]>([]);
  // The latest list, for Apply all and Dismiss all between one request and
  // the next, and for telling a newly applied shortcut from one applied
  // before (a refetch brings those back too).
  const proposalsRef = useRef(proposals);
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set());
  // Mirrors busy for Apply all and Dismiss all, which read it between one
  // request and the next.
  const busyRef = useRef(busy);
  const [errors, setErrors] = useState<ReadonlyMap<number, string>>(new Map());
  const [bulk, setBulk] = useState<LearnBulk | null>(null);
  // A bulk run left going would keep applying after the layout (keyed by
  // channel) unmounted.
  const unmountedRef = useRef(false);
  useEffect(() => {
    unmountedRef.current = false;
    return () => {
      unmountedRef.current = true;
    };
  }, []);
  const [shortcutsVersion, setShortcutsVersion] = useState(0);
  const [passes, setPasses] = useState<LearnPass[]>([]);
  const mergePasses = useCallback((incoming: LearnPass[]) => setPasses((cur) => mergeLearnPasses(cur, incoming)), []);
  const [focus, setFocus] = useState<LearnView["focus"]>(null);
  const seqRef = useRef(0);
  // Folds proposals into the list; noteApplied: an applied shortcut among
  // them makes the pickers fetch theirs again.
  const merge = useCallback((incoming: LearnProposal[], noteApplied = true) => {
    const cur = proposalsRef.current;
    proposalsRef.current = mergeProposals(cur, incoming);
    setProposals(proposalsRef.current);
    if (noteApplied && newlyAppliedShortcut(cur, incoming)) setShortcutsVersion((n) => n + 1);
  }, []);

  // Fetched on mount, and again on each reconnect: the events missed while
  // the WS was down are lost (a pass that ended would stay "learning…" and
  // the proposals it filed would be missing), so what the server says then
  // wins. The first open (from 0) isn't a reconnect.
  const wsOpensRef = useRef(wsOpens);
  useEffect(() => {
    const prev = wsOpensRef.current;
    wsOpensRef.current = wsOpens;
    const isReconnect = prev !== wsOpens && prev !== 0;
    // Until the state loads the Learn switch stays hidden, so a failed
    // fetch is tried again rather than hiding it for good.
    const cancelState = fetchWithRetry(
      () => fetchLearnState(channelId),
      "fetching learn state",
      (st) => {
        setAvailable(st.available);
        setLearnValue(st.learn);
        setDefaultLearn(st.default_learn);
        // On mount, a learn.started that arrived while this was in flight is
        // newer: don't let the fetched state undo it.
        setLearnChannelId((cur) => (isReconnect ? st.learn_channel_id || cur : cur || st.learn_channel_id));
        setRunning((cur) => (isReconnect ? st.running : cur || st.running));
        setLoaded(true);
      },
    );
    const cancelProposals = fetchWithRetry(
      () => fetchLearnProposals(channelId),
      "fetching learn proposals",
      (list) => merge(list, isReconnect),
    );
    const cancelPasses = fetchWithRetry(() => fetchLearnPasses(channelId), "fetching learn passes", mergePasses);
    return () => {
      cancelState();
      cancelProposals();
      cancelPasses();
    };
  }, [channelId, wsOpens, merge, mergePasses]);

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
          merge(learnProposalsEventItems(event.data as { proposals: LearnProposal[]; withdrawn?: LearnProposal[] }));
          break;
        case "learn.proposal_updated":
          merge([event.data as LearnProposal]);
          break;
        case "learn.pass":
          mergePasses([event.data as LearnPass]);
          break;
        default:
      }
    });
  }, [channelId, subscribeChatEvents, merge, mergePasses]);

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

  const settle = useCallback(
    (id: number, action: (id: number) => Promise<LearnProposal>) =>
      settleProposal(id, action, {
        busy: {
          get: () => busyRef.current,
          set: (next) => {
            busyRef.current = next;
            setBusy(next);
          },
        },
        setError: (id, error) => setErrors((cur) => withError(cur, id, error)),
        settled: (p) => merge([p]),
      }),
    [merge],
  );
  const apply = useCallback((id: number) => settle(id, applyLearnProposal), [settle]);
  const dismiss = useCallback((id: number) => settle(id, dismissLearnProposal), [settle]);

  // Apply all and Dismiss all are disabled while either runs (see bulk).
  const bulkAll = useCallback(
    async (kind: LearnBulk) => {
      setBulk(kind);
      const action = kind === "apply" ? applyLearnProposal : dismissLearnProposal;
      await runBulk(
        kind,
        () => proposalsRef.current,
        () => busyRef.current,
        (id) => settle(id, action),
        () => unmountedRef.current,
      );
      setBulk(null);
    },
    [settle],
  );
  const applyAll = useCallback(() => bulkAll("apply"), [bulkAll]);

  const show = useCallback(
    (messageId: string) => {
      seqRef.current += 1;
      setFocus({ messageId, seq: seqRef.current });
      onShow?.();
    },
    [onShow],
  );
  const passByMessage = useMemo(() => learnPassesByMessage(passes), [passes]);

  const [turnBusy, setTurnBusy] = useState<ReadonlySet<string>>(new Set());
  const [turnErrors, setTurnErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const learnTurn = useCallback(
    async (messageId: string) => {
      setTurnBusy((cur) => new Set(cur).add(messageId));
      setTurnErrors((cur) => withoutTurn(cur, messageId));
      show(messageId);
      try {
        mergePasses([await postLearnTurn(channelId, messageId)]);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        setTurnErrors((cur) => new Map(cur).set(messageId, msg));
      } finally {
        setTurnBusy((cur) => {
          const next = new Set(cur);
          next.delete(messageId);
          return next;
        });
      }
    },
    [channelId, mergePasses, show],
  );
  const dismissAll = useCallback(() => bulkAll("dismiss"), [bulkAll]);

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
      passes,
      passByMessage,
      focus,
      show,
      learnTurn,
      turnBusy,
      turnErrors,
    }),
    [
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
      passes,
      passByMessage,
      focus,
      show,
      learnTurn,
      turnBusy,
      turnErrors,
    ],
  );
}

function withoutTurn(errors: ReadonlyMap<string, string>, messageId: string): ReadonlyMap<string, string> {
  if (!errors.has(messageId)) return errors;
  const next = new Map(errors);
  next.delete(messageId);
  return next;
}

function withError(errors: ReadonlyMap<number, string>, id: number, error: string | undefined): ReadonlyMap<number, string> {
  if (errors.get(id) === error) return errors;
  const next = new Map(errors);
  if (error === undefined) next.delete(id);
  else next.set(id, error);
  return next;
}
