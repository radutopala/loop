import { useCallback, useEffect, useRef, useState } from "react";
import type { Message, TimelineCursor, TimelineItem } from "../types";
import { fetchTimeline } from "../api/loopApi";

const PAGE_SIZE = 50;

interface UseTimelineResult {
  items: TimelineItem[];
  liveTail: TimelineItem[];
  loading: boolean;
  loadMore: () => void;
  hasMore: boolean;
  // Live SSE handlers — append to the live tail.
  appendLiveMessage: (msg: Message) => void;
  appendLiveThinking: (text: string) => void;
  appendLiveToolUse: (toolUseID: string | undefined, toolName: string, input: string) => void;
  appendLiveToolResult: (toolUseID: string | undefined, output: string, isError: boolean) => void;
  appendLiveCompacting: () => void;
  // Mutators for chat-row events that already affected DB state.
  markProcessed: (msgIds: string[]) => void;
  removeMessage: (msgId: string) => void;
  // Refetch the head of the timeline + drop the live tail (called on run completion).
  refetchHead: () => void;
}

/**
 * useTimeline mirrors useMessages but returns interleaved chat messages and
 * agent events fetched from /api/channels/{id}/timeline. Live SSE events are
 * accumulated in a separate liveTail buffer; on run completion the caller
 * refetches the head so backfilled items replace the buffered ones.
 */
export function useTimeline(channelId: string | null): UseTimelineResult {
  const [items, setItems] = useState<TimelineItem[]>([]);
  const [liveTail, setLiveTail] = useState<TimelineItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const cursorRef = useRef<TimelineCursor | null>(null);
  const loadingRef = useRef(false);
  const itemsRef = useRef<TimelineItem[]>([]);
  itemsRef.current = items;
  // Per-hook-instance counter producing strictly-decreasing synthetic ids for
  // live-tail items. Negative so they never collide with backend row ids.
  const liveCounterRef = useRef(0);
  const nextLiveId = useCallback((): number => {
    liveCounterRef.current -= 1;
    return liveCounterRef.current;
  }, []);

  // Reset and fetch first page when the channel changes.
  useEffect(() => {
    setItems([]);
    setLiveTail([]);
    setHasMore(false);
    cursorRef.current = null;
    loadingRef.current = false;

    if (!channelId) return;

    let cancelled = false;
    setLoading(true);
    loadingRef.current = true;

    fetchTimeline(channelId, { limit: PAGE_SIZE })
      .then((resp) => {
        if (cancelled) return;
        // /timeline returns DESC by chain_position; flip to ASC for top→bottom render.
        setItems([...resp.items].reverse());
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
      })
      .catch(() => {
        /* falls back to empty list; live SSE may still populate */
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
          loadingRef.current = false;
        }
      });

    return () => {
      cancelled = true;
    };
  }, [channelId]);

  const loadMore = useCallback(() => {
    if (!channelId || loadingRef.current || !hasMore || !cursorRef.current) return;
    setLoading(true);
    loadingRef.current = true;
    fetchTimeline(channelId, {
      limit: PAGE_SIZE,
      cursorPosition: cursorRef.current.position,
      cursorId: cursorRef.current.id,
    })
      .then((resp) => {
        const older = [...resp.items].reverse();
        setItems((prev) => [...older, ...prev]);
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
      })
      .catch(() => {})
      .finally(() => {
        setLoading(false);
        loadingRef.current = false;
      });
  }, [channelId, hasMore]);

  const appendLiveMessage = useCallback((msg: Message) => {
    setLiveTail((prev) => {
      // Dedup by msg_id — message.created can fire twice across reconnect/resume.
      if (prev.some((it) => it.kind === "message" && it.data.msg_id === msg.msg_id)) return prev;
      return [...prev, { kind: "message", position: 0, id: nextLiveId(), data: msg }];
    });
  }, [nextLiveId]);

  const appendLiveThinking = useCallback((text: string) => {
    setLiveTail((prev) => [
      ...prev,
      { kind: "thinking", position: 0, id: nextLiveId(), text, truncated: false },
    ]);
  }, [nextLiveId]);

  const appendLiveToolUse = useCallback(
    (toolUseID: string | undefined, toolName: string, input: string) => {
      setLiveTail((prev) => [
        ...prev,
        {
          kind: "tool_use",
          position: 0,
          id: nextLiveId(),
          tool_use_id: toolUseID ?? "",
          tool_name: toolName,
          tool_input: input,
        },
      ]);
    },
    [nextLiveId],
  );

  const appendLiveToolResult = useCallback(
    (toolUseID: string | undefined, output: string, isError: boolean) => {
      setLiveTail((prev) => [
        ...prev,
        {
          kind: "tool_result",
          position: 0,
          id: nextLiveId(),
          tool_use_id: toolUseID ?? "",
          text: output,
          is_error: isError,
        },
      ]);
    },
    [nextLiveId],
  );

  const appendLiveCompacting = useCallback(() => {
    setLiveTail((prev) => {
      // Coalesce repeated compacting events: if the last item is already a
      // compacting marker, skip — the runner can emit the status multiple times
      // during a single /compact pass.
      const last = prev[prev.length - 1];
      if (last && last.kind === "compacting") return prev;
      return [...prev, { kind: "compacting", position: 0, id: nextLiveId() }];
    });
  }, [nextLiveId]);

  const markProcessed = useCallback((msgIds: string[]) => {
    if (msgIds.length === 0) return;
    const idSet = new Set(msgIds);
    const apply = (list: TimelineItem[]): TimelineItem[] => {
      let changed = false;
      const next = list.map((it) => {
        if (it.kind !== "message") return it;
        if (!idSet.has(it.data.msg_id) || it.data.is_processed) return it;
        changed = true;
        return { ...it, data: { ...it.data, is_processed: true } };
      });
      return changed ? next : list;
    };
    setItems(apply);
    setLiveTail(apply);
  }, []);

  const removeMessage = useCallback((msgId: string) => {
    const filter = (list: TimelineItem[]): TimelineItem[] => {
      const next = list.filter((it) => !(it.kind === "message" && it.data.msg_id === msgId));
      return next.length === list.length ? list : next;
    };
    setItems(filter);
    setLiveTail(filter);
  }, []);

  const refetchHead = useCallback(() => {
    if (!channelId) return;
    // A long run can backfill hundreds of event rows past the first PAGE_SIZE.
    // Walk backwards until the oldest fresh page reaches the previous head,
    // capped so we never wedge the UI on a 1000-event run.
    const GAP_PAGE_BUDGET = 5;

    let bridgeCp = -1;
    let bridgeId = -1;
    for (const it of itemsRef.current) {
      if (it.position > bridgeCp || (it.position === bridgeCp && it.id > bridgeId)) {
        bridgeCp = it.position;
        bridgeId = it.id;
      }
    }

    const run = async () => {
      const collected: TimelineItem[] = [];
      let cursor: TimelineCursor | null = null;
      let nextCursor: TimelineCursor | null = null;
      for (let page = 0; page < GAP_PAGE_BUDGET; page += 1) {
        const resp = await fetchTimeline(channelId, {
          limit: PAGE_SIZE,
          ...(cursor ? { cursorPosition: cursor.position, cursorId: cursor.id } : {}),
        });
        nextCursor = resp.next_cursor;
        if (resp.items.length === 0) break;
        // /timeline returns DESC by chain_position.
        collected.push(...resp.items);
        const oldest = resp.items[resp.items.length - 1]!;
        const reached =
          oldest.position < bridgeCp ||
          (oldest.position === bridgeCp && oldest.id <= bridgeId);
        if (reached || resp.next_cursor === null) break;
        cursor = resp.next_cursor;
      }

      if (collected.length === 0) {
        setLiveTail([]);
        return;
      }

      const fresh = collected.slice().reverse();
      const oldestFreshPos = fresh[0]!.position;
      const oldestFreshId = fresh[0]!.id;
      setItems((prev) => {
        const tail = prev.filter((it) => {
          if (it.position > oldestFreshPos) return false;
          if (it.position === oldestFreshPos && it.id >= oldestFreshId) return false;
          return true;
        });
        return [...tail, ...fresh];
      });
      setHasMore(nextCursor !== null);
      cursorRef.current = nextCursor;
      setLiveTail([]);
    };

    run().catch(() => {});
  }, [channelId]);

  return {
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
  };
}
