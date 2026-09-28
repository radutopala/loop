import { createContext, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type ExplainState, type Explanation, explainTurn, fetchExplainState, fetchExplanations, setExplain as putExplain } from "../api/explain";
import { mergeExplanations } from "../components/chat/explainState";
import type { WSEvent } from "../types";
import { logErr } from "../utils/log";
import type { ChatEventListener } from "./useChatStateStore";
import { fetchWithRetry } from "./useLearn";

export interface ExplainView {
  /** False until the channel's explain state has loaded. */
  loaded: boolean;
  /** False for Slack and Discord channels and task threads, never explained. */
  available: boolean;
  /** The channel's own switch: "on", "off", or "" to follow defaultExplain. */
  explain: ExplainState["explain"];
  defaultExplain: boolean;
  setExplain: (explain: ExplainState["explain"]) => Promise<void>;
  /** The channel's explanations, newest first. */
  explanations: Explanation[];
  /** The explanation of each explained turn, by its last bot message's msg_id. */
  byMessage: ReadonlyMap<string, Explanation>;
  /** Turns with an explain request in flight, by msg_id. */
  busy: ReadonlySet<string>;
  /** A request that failed, by msg_id, until the next try. */
  errors: ReadonlyMap<string, string>;
  /** The explanation to bring into view in the Explain pane; seq changes on
   * each request, so asking for the same one again scrolls to it again. */
  focus: { messageId: string; seq: number } | null;
  /** Explain a turn (force: again), then show it in the Explain pane. */
  explainTurn: (messageId: string, force?: boolean) => Promise<void>;
  /** Show a turn's explanation in the Explain pane. */
  show: (messageId: string) => void;
}

/** The selected channel's ExplainView, for the chat's Explain actions. */
export const ExplainContext = createContext<ExplainView | null>(null);

/**
 * Asks the channel's layout to show its Explain pane, adding it beside the
 * chat when it isn't in the layout (see WorkspaceLayout's loop:open-panel).
 */
export function openExplainPane(channelId: string): void {
  window.dispatchEvent(new CustomEvent("loop:open-panel", { detail: { channelId, panel: "explain", anchorPanel: "chat" } }));
}

/**
 * Follows a channel's explanations: its explain switch, the write-ups and
 * the explain requests in flight. The channel.explain and explain.updated
 * events are global and carry the explained channel's id, so they reach the
 * selected channel's listeners. Events missed while the WS was down are made
 * up for by fetching again on each reconnect (wsOpens).
 */
export function useExplain(channelId: string, subscribeChatEvents?: (listener: ChatEventListener) => () => void, wsOpens = 0): ExplainView {
  const [loaded, setLoaded] = useState(false);
  const [available, setAvailable] = useState(false);
  const [explain, setExplainValue] = useState<ExplainState["explain"]>("");
  const [defaultExplain, setDefaultExplain] = useState(false);
  const [explanations, setExplanations] = useState<Explanation[]>([]);
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  const [errors, setErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const [focus, setFocus] = useState<ExplainView["focus"]>(null);
  const seqRef = useRef(0);

  const merge = useCallback((incoming: Explanation[]) => setExplanations((cur) => mergeExplanations(cur, incoming)), []);

  useEffect(() => {
    const cancelState = fetchWithRetry(
      () => fetchExplainState(channelId),
      "fetching explain state",
      (st) => {
        setAvailable(st.available);
        setExplainValue(st.explain);
        setDefaultExplain(st.default_explain);
        setLoaded(true);
      },
    );
    const cancelList = fetchWithRetry(() => fetchExplanations(channelId), "fetching explanations", merge);
    return () => {
      cancelState();
      cancelList();
    };
  }, [channelId, wsOpens, merge]);

  useEffect(() => {
    if (!subscribeChatEvents) return;
    return subscribeChatEvents((event: WSEvent) => {
      if (event.channel_id !== channelId) return;
      if (event.type === "channel.explain") setExplainValue((event.data as { explain: ExplainState["explain"] }).explain);
      else if (event.type === "explain.updated") merge([event.data as Explanation]);
    });
  }, [channelId, subscribeChatEvents, merge]);

  const setExplain = useCallback(
    async (next: ExplainState["explain"]) => {
      const prev = explain;
      setExplainValue(next);
      try {
        await putExplain(channelId, next);
      } catch (e) {
        setExplainValue(prev);
        logErr("setting explain")(e);
      }
    },
    [channelId, explain],
  );

  const show = useCallback(
    (messageId: string) => {
      seqRef.current += 1;
      setFocus({ messageId, seq: seqRef.current });
      openExplainPane(channelId);
    },
    [channelId],
  );

  const run = useCallback(
    async (messageId: string, force = false) => {
      setBusy((cur) => new Set(cur).add(messageId));
      setErrors((cur) => withoutKey(cur, messageId));
      show(messageId);
      try {
        merge([await explainTurn(channelId, messageId, force)]);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        setErrors((cur) => new Map(cur).set(messageId, msg));
      } finally {
        setBusy((cur) => {
          const next = new Set(cur);
          next.delete(messageId);
          return next;
        });
      }
    },
    [channelId, merge, show],
  );

  const byMessage = useMemo(() => new Map(explanations.map((e) => [e.message_id, e])), [explanations]);

  return useMemo(
    () => ({ loaded, available, explain, defaultExplain, setExplain, explanations, byMessage, busy, errors, focus, explainTurn: run, show }),
    [loaded, available, explain, defaultExplain, setExplain, explanations, byMessage, busy, errors, focus, run, show],
  );
}

function withoutKey(m: ReadonlyMap<string, string>, key: string): ReadonlyMap<string, string> {
  if (!m.has(key)) return m;
  const next = new Map(m);
  next.delete(key);
  return next;
}
