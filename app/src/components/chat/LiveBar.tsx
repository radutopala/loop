import { useCallback, useEffect, useRef, useState } from "react";
import { fetchLive, type LiveStatus, warmLive } from "../../api/live";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { useTheme } from "../../ThemeContext";
import { logErr } from "../../utils/log";
import { applyLiveEvent, liveSummary, shouldWarm } from "./liveSession";

// The last live status seen per channel, so a chat opened again shows it at
// once instead of starting blank and flickering when the fetch lands.
const lastLive = new Map<string, LiveStatus>();

/**
 * useLiveStatus follows the channel's live session: the last status seen for
 * it, then fetched when the chat opens and on each WS (re)connect, and kept
 * up to date from its events. Without subscribeChatEvents (a read-only or
 * Learn thread) there's none.
 */
export function useLiveStatus(channelId: string | null, subscribeChatEvents?: (listener: ChatEventListener) => () => void, wsOpens?: number): LiveStatus | null {
  const following = !!channelId && !!subscribeChatEvents;
  const [status, setStatus] = useState<LiveStatus | null>(() => (following ? (lastLive.get(channelId) ?? null) : null));

  useEffect(() => {
    setStatus(following ? (lastLive.get(channelId) ?? null) : null);
    if (!channelId || !following) return;
    let live = true;
    fetchLive(channelId)
      .then((s) => {
        if (!live) return;
        lastLive.set(channelId, s);
        setStatus(s);
      })
      .catch(logErr("fetching the live session"));
    return () => {
      live = false;
    };
  }, [channelId, following, wsOpens]);

  useEffect(() => {
    if (!channelId || !subscribeChatEvents) return;
    return subscribeChatEvents((event) =>
      setStatus((s) => {
        const next = applyLiveEvent(s, event, channelId);
        if (next && next !== s) lastLive.set(channelId, next);
        return next;
      }),
    );
  }, [subscribeChatEvents, channelId]);

  return status;
}

/** useWarmLive returns what the composer calls as the user types: it starts
 *  the channel's live session ahead of the turn, once per session. */
export function useWarmLive(channelId: string | null, status: LiveStatus | null): () => void {
  const asked = useRef(false);
  const running = !!status?.running;
  useEffect(() => {
    if (running) asked.current = false;
  }, [running]);
  return useCallback(() => {
    if (!channelId || !shouldWarm(status, asked.current)) return;
    asked.current = true;
    warmLive(channelId).catch(logErr("starting the live session"));
  }, [channelId, status]);
}

/** LiveBar shows above the composer why the live session ended, when that
 *  needs saying; BackgroundTasksBar shows the tasks it waits on, and the
 *  footer's icon whether it's working or idle. */
export function LiveBar({ status }: { status: LiveStatus | null }) {
  const { colors } = useTheme();
  const ended = liveSummary(status);
  if (!ended) return null;
  return (
    <div data-testid="live-bar" style={{ padding: "6px 24px 0" }}>
      <div
        title={ended}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          // As wide as the composer under it.
          maxWidth: 768,
          margin: "0 auto",
          boxSizing: "border-box",
          padding: "4px 10px",
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          background: colors.surface,
          fontSize: 12,
          color: colors.textMuted,
        }}
      >
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: colors.text }}>{ended}</span>
      </div>
    </div>
  );
}
