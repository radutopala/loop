import { useCallback, useEffect, useRef, useState } from "react";
import type { Message } from "../types";
import { fetchMessages } from "../api/loopApi";

const PAGE_SIZE = 50;

interface UseMessagesResult {
  messages: Message[];
  loading: boolean;
  loadMore: () => void;
  hasMore: boolean;
  addMessage: (msg: Message) => void;
}

export function useMessages(channelId: string | null, aroundMessageId?: number | null): UseMessagesResult {
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const cursorRef = useRef<number | null>(null);
  const loadingRef = useRef(false);
  const aroundRef = useRef(aroundMessageId);
  aroundRef.current = aroundMessageId;

  // Reset when channel changes (or component re-mounts).
  useEffect(() => {
    setMessages([]);
    setHasMore(false);
    cursorRef.current = null;
    loadingRef.current = false;

    if (!channelId) return;

    let cancelled = false;
    setLoading(true);
    loadingRef.current = true;

    const around = aroundRef.current;
    const opts = around
      ? { limit: PAGE_SIZE, around }
      : { limit: PAGE_SIZE };

    fetchMessages(channelId, opts)
      .then((resp) => {
        if (cancelled) return;
        // "around" returns messages in ASC order already; cursor-based returns DESC.
        const sorted = around ? resp.messages : [...resp.messages].reverse();
        setMessages(sorted);
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
        // For "around" mode, set cursor to the oldest message so loadMore works.
        if (around && sorted.length > 0) {
          cursorRef.current = sorted[0]!.id;
          setHasMore(true); // assume there are older messages
        }
      })
      .catch(() => {
        /* will retry via event stream */
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
    if (!channelId || loadingRef.current || !hasMore || cursorRef.current === null) return;

    setLoading(true);
    loadingRef.current = true;
    fetchMessages(channelId, { limit: PAGE_SIZE, cursor: cursorRef.current })
      .then((resp) => {
        const older = [...resp.messages].reverse();
        setMessages((prev) => [...older, ...prev]);
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
      })
      .catch(() => {})
      .finally(() => {
        setLoading(false);
        loadingRef.current = false;
      });
  }, [channelId, hasMore]);

  const addMessage = useCallback((msg: Message) => {
    setMessages((prev) => {
      // Deduplicate by msg_id.
      if (prev.some((m) => m.msg_id === msg.msg_id)) return prev;
      return [...prev, msg];
    });
  }, []);

  return { messages, loading, loadMore, hasMore, addMessage };
}
