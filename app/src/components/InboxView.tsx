import { useCallback, useEffect, useRef, useState } from "react";
import { ATTENTION_CHANGED, type AttentionItem, attentionActions, attentionKey, fetchAttention, groupAttention, needsFlash, nextSeen, runAttentionAction } from "../api/attention";
import { NOTIFICATIONS_CHANGED } from "../api/notifications";
import { useWebSocketConnection } from "../hooks/useWebSocketConnection";
import { useTheme } from "../ThemeContext";
import { fonts } from "../theme";
import { storageGet, storageSet } from "../utils/storage";

const PINNED_KEY = "loop-inbox-pinned";
// How long a burst of events waits before the list is read again.
const REFETCH_DEBOUNCE_MS = 300;
// PR states change without an event reaching here every time.
const POLL_MS = 30_000;
// The events after which the list may have changed. A deleted channel takes
// its items with it.
const REFETCH_ON = new Set([ATTENTION_CHANGED, NOTIFICATIONS_CHANGED, "channel.deleted"]);

const KIND_LABEL: Record<string, string> = {
  ask: "Question",
  plan: "Plan",
  gate: "Approval",
  pr: "PR",
  run: "Run",
};

/** Whether the page is the menu bar inbox's. */
export function isInboxWindow(search = window.location.search): boolean {
  return new URLSearchParams(search).get("window") === "inbox";
}

/**
 * The menu bar inbox: what waits on the owner across every project, grouped
 * by channel. It tells the tray how many there are, and to flash while a
 * blocking one hasn't been seen here.
 */
export default function InboxView() {
  const { colors } = useTheme();
  const [items, setItems] = useState<AttentionItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [pinned, setPinned] = useState(() => storageGet(PINNED_KEY) === "1");
  const [shown, setShown] = useState(false);
  const seen = useRef<Set<string>>(new Set());
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const load = useCallback(() => {
    fetchAttention()
      .then((list) => {
        setItems(list);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  const loadSoon = useCallback(() => {
    clearTimeout(timer.current);
    timer.current = setTimeout(load, REFETCH_DEBOUNCE_MS);
  }, [load]);

  useWebSocketConnection({
    path: "/api/ws",
    enabled: true,
    onOpen: useCallback(
      (ws: WebSocket) => {
        // No channel has this id, so only the global events come through.
        ws.send(JSON.stringify({ type: "subscribe", channels: ["inbox"] }));
        load();
      },
      [load],
    ),
    onMessage: useCallback(
      (event: MessageEvent) => {
        if (typeof event.data !== "string") return;
        try {
          if (REFETCH_ON.has(JSON.parse(event.data).type)) loadSoon();
        } catch {
          // Not an event.
        }
      },
      [loadSoon],
    ),
  });

  useEffect(() => {
    const id = setInterval(load, POLL_MS);
    return () => {
      clearInterval(id);
      clearTimeout(timer.current);
    };
  }, [load]);

  useEffect(() => window.loopAPI?.onInboxShown?.(setShown), []);

  useEffect(() => {
    seen.current = nextSeen(items, seen.current, shown);
    window.loopAPI?.inboxReport?.({ count: items.length, flash: needsFlash(items, seen.current) });
  }, [items, shown]);

  useEffect(() => {
    storageSet(PINNED_KEY, pinned ? "1" : "0");
    window.loopAPI?.setInboxPinned?.(pinned);
  }, [pinned]);

  // Where a header drag started: the pointer and the window, in screen points.
  const drag = useRef<{ px: number; py: number; wx: number; wy: number } | null>(null);
  const dragHandlers = {
    onPointerDown: (e: React.PointerEvent<HTMLDivElement>) => {
      if (e.button !== 0 || (e.target as HTMLElement).closest("button")) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      drag.current = { px: e.screenX, py: e.screenY, wx: window.screenX, wy: window.screenY };
    },
    onPointerMove: (e: React.PointerEvent<HTMLDivElement>) => {
      const d = drag.current;
      if (d) window.loopAPI?.moveInbox?.(d.wx + e.screenX - d.px, d.wy + e.screenY - d.py);
    },
    onPointerUp: () => {
      drag.current = null;
    },
    onPointerCancel: () => {
      drag.current = null;
    },
  };

  const [busy, setBusy] = useState<Set<string>>(new Set());

  // Takes the action and drops the item, which goes once its cause does; a
  // failure keeps it, with the error shown.
  const act = (it: AttentionItem, id: string) => {
    const key = attentionKey(it);
    setBusy((b) => new Set(b).add(key));
    runAttentionAction(it, id)
      .then(() => setItems((list) => list.filter((x) => attentionKey(x) !== key)))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => {
        setBusy((b) => {
          const next = new Set(b);
          next.delete(key);
          return next;
        });
        loadSoon();
      });
  };

  const button = {
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 6,
    color: colors.textMuted,
    cursor: "pointer",
    fontSize: 11,
    padding: "2px 8px",
  } as const;

  const groups = groupAttention(items);
  const blocking = items.filter((it) => it.blocking).length;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100vh",
        fontFamily: fonts.sans,
        background: colors.bg,
        color: colors.text,
        fontSize: 13,
        border: `1px solid ${colors.border}`,
        borderRadius: 10,
        overflow: "hidden",
        boxSizing: "border-box",
      }}
    >
      <div {...dragHandlers} style={{ display: "flex", alignItems: "center", gap: 8, padding: "10px 12px", borderBottom: `1px solid ${colors.border}`, cursor: "grab", userSelect: "none" }}>
        <div style={{ flex: 1, fontWeight: 600 }}>
          Needs you
          {items.length > 0 && (
            <span style={{ color: colors.textMuted, fontWeight: 400 }}>
              {" "}
              · {items.length}
              {blocking > 0 ? ` (${blocking} blocking)` : ""}
            </span>
          )}
        </div>
        <button
          type="button"
          data-testid="inbox-pin"
          aria-pressed={pinned}
          title={pinned ? "Unpin: hide when it loses focus" : "Pin: keep it open when it loses focus"}
          onClick={() => setPinned((p) => !p)}
          style={{ ...button, color: pinned ? colors.active : colors.textMuted, borderColor: pinned ? colors.active : colors.border }}
        >
          {pinned ? "Pinned" : "Pin"}
        </button>
        <button type="button" title="Close" aria-label="Close" onClick={() => window.loopAPI?.closeInbox?.()} style={{ ...button, border: "none", fontSize: 14 }}>
          ×
        </button>
      </div>
      {error && <div style={{ padding: "8px 12px", background: colors.errorBannerBg, color: colors.errorBannerText, fontSize: 12 }}>{error}</div>}
      <div style={{ flex: 1, overflowY: "auto", padding: "4px 0" }}>
        {groups.length === 0 && !error && <div style={{ padding: 24, textAlign: "center", color: colors.textMuted }}>Nothing needs you.</div>}
        {groups.map((g) => (
          <div key={g.channelId} style={{ padding: "6px 0" }}>
            <div style={{ padding: "2px 12px", color: colors.textMuted, fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{g.label}</div>
            {g.items.map((it) => {
              const actions = attentionActions(it);
              return (
                <div
                  key={attentionKey(it)}
                  data-testid="inbox-item"
                  role="button"
                  tabIndex={0}
                  onClick={() => window.loopAPI?.openChannelFromInbox?.(it.channel_id)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") window.loopAPI?.openChannelFromInbox?.(it.channel_id);
                  }}
                  style={{ display: "flex", gap: 8, alignItems: "flex-start", padding: "6px 12px", cursor: "pointer" }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = colors.hoverBg;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = "";
                  }}
                >
                  <span
                    style={{
                      flexShrink: 0,
                      marginTop: 2,
                      fontSize: 10,
                      padding: "1px 6px",
                      borderRadius: 4,
                      background: it.blocking ? colors.warning : colors.selectedBg,
                      color: it.blocking ? colors.bg : colors.textLight,
                    }}
                  >
                    {KIND_LABEL[it.kind] ?? it.kind}
                  </span>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{it.title}</div>
                    {it.detail && (
                      <div style={{ color: colors.textMuted, fontSize: 12, whiteSpace: "pre-wrap", overflow: "hidden", display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical" }}>
                        {it.detail}
                      </div>
                    )}
                    {actions.length > 0 && (
                      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 6 }}>
                        {actions.map((a) => (
                          <button
                            key={a.id}
                            type="button"
                            data-testid="inbox-action"
                            disabled={busy.has(attentionKey(it))}
                            onClick={(e) => {
                              e.stopPropagation();
                              act(it, a.id);
                            }}
                            onKeyDown={(e) => e.stopPropagation()}
                            style={{
                              ...button,
                              fontSize: 12,
                              padding: "3px 10px",
                              opacity: busy.has(attentionKey(it)) ? 0.5 : 1,
                              ...(a.variant === "primary" && { background: colors.active, borderColor: colors.active, color: colors.bg }),
                              ...(a.variant === "danger" && { color: colors.error, borderColor: colors.error }),
                            }}
                          >
                            {a.label}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
