import { useCallback, useEffect, useRef, useState } from "react";
import { fetchLive, type LiveStatus, warmLive } from "../../api/live";
import { reclaimDockerSpace } from "../../api/loopApi";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { useTheme } from "../../ThemeContext";
import { logErr } from "../../utils/log";
import { reclaimSummary } from "../shared/Settings";
import { applyLiveEvent, liveDiskFull, liveSummary, shouldWarm } from "./liveSession";

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
 *  footer's icon whether it's working or idle. A session that ended on a full
 *  disk can reclaim Docker's space from here, as Settings does by default;
 *  onReclaimSettings opens that section, for its estimate and options. */
export function LiveBar({ status, onReclaimSettings }: { status: LiveStatus | null; onReclaimSettings?: () => void }) {
  const { colors } = useTheme();
  const [reclaiming, setReclaiming] = useState(false);
  const [reclaimed, setReclaimed] = useState<string | null>(null);
  const [reclaimErr, setReclaimErr] = useState<string | null>(null);
  // What was reclaimed answers this ending, not the next.
  useEffect(() => {
    setReclaimed(null);
    setReclaimErr(null);
  }, [status]);

  const ended = liveSummary(status);
  if (!ended) return null;
  const reclaim = () => {
    setReclaiming(true);
    setReclaimErr(null);
    reclaimDockerSpace(false)
      .then((r) => setReclaimed(`${reclaimSummary(r)} Send your message again.`))
      .catch((e) => setReclaimErr(e?.message ?? "Reclaim failed"))
      .finally(() => setReclaiming(false));
  };
  const button = { flexShrink: 0, padding: "2px 8px", border: `1px solid ${colors.border}`, borderRadius: 4, background: colors.bg, color: colors.text, fontSize: 12, fontFamily: "inherit" };
  return (
    <div data-testid="live-bar" style={{ padding: "6px 24px 0" }}>
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
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
        {/* In full: it can be the process's own error. */}
        <div style={{ flex: 1, minWidth: 0, maxHeight: 120, overflowY: "auto", whiteSpace: "pre-wrap", overflowWrap: "anywhere", color: colors.text }}>
          <div data-testid="live-bar-reason">{ended}</div>
          {reclaimed && <div data-testid="live-bar-reclaimed">{reclaimed}</div>}
          {reclaimErr && <div style={{ color: colors.error }}>{reclaimErr}</div>}
        </div>
        {liveDiskFull(status) && !reclaimed && (
          <button data-testid="live-bar-reclaim" disabled={reclaiming} onClick={reclaim} style={{ ...button, cursor: reclaiming ? "default" : "pointer" }}>
            {reclaiming ? "Reclaiming…" : "Reclaim Docker space"}
          </button>
        )}
        {liveDiskFull(status) && onReclaimSettings && (
          <button data-testid="live-bar-reclaim-settings" onClick={onReclaimSettings} style={{ ...button, cursor: "pointer" }}>
            Settings
          </button>
        )}
      </div>
    </div>
  );
}
