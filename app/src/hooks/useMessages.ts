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

export function useMessages(channelId: string | null): UseMessagesResult {
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const cursorRef = useRef<number | null>(null);

  // Reset when channel changes.
  useEffect(() => {
    setMessages([]);
    setHasMore(false);
    cursorRef.current = null;

    if (!channelId) return;

    let cancelled = false;
    setLoading(true);

    fetchMessages(channelId, { limit: PAGE_SIZE })
      .then((resp) => {
        if (cancelled) return;
        setMessages(resp.messages.reverse());
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
      })
      .catch(() => {
        /* will retry via event stream */
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [channelId]);

  const loadMore = useCallback(() => {
    if (!channelId || loading || !hasMore || cursorRef.current === null) return;

    setLoading(true);
    fetchMessages(channelId, { limit: PAGE_SIZE, cursor: cursorRef.current })
      .then((resp) => {
        setMessages((prev) => [...resp.messages.reverse(), ...prev]);
        setHasMore(resp.next_cursor !== null);
        cursorRef.current = resp.next_cursor;
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [channelId, loading, hasMore]);

  const addMessage = useCallback((msg: Message) => {
    setMessages((prev) => {
      // Deduplicate by msg_id.
      if (prev.some((m) => m.msg_id === msg.msg_id)) return prev;
      return [...prev, msg];
    });
  }, []);

  return { messages, loading, loadMore, hasMore, addMessage };
}
