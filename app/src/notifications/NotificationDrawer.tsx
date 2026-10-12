import { useEffect, useRef, useState } from "react";
import { clearNotifications, deleteNotification, fetchNotifications, markNotificationsRead, type NotificationInfo } from "../api/notifications";
import { fetchPlugins, PLUGINS_CHANGED, type PluginInfo } from "../api/plugins";
import { prefersReducedMotion, usePresence } from "../hooks/usePresence";
import { PluginCard } from "../plugins/PluginCard";
import { buildBtnStyle } from "../splitPane/PaneLeafHeader";
import { useTheme } from "../ThemeContext";
import { fonts } from "../theme";
import { logErr } from "../utils/log";
import { timeAgo } from "../utils/workflowHelpers";
import { newestId, notificationHeading, notificationSource, unreadLabel, unreadTitle } from "./notifications";
import { useNotificationsState } from "./useNotifications";

/** The layout bar's button that opens and closes the channel's drawer,
 *  with its unread count. */
export function NotificationButton({ channelId, open, onToggle }: { channelId: string; open: boolean; onToggle: () => void }) {
  const { colors } = useTheme();
  const unread = useNotificationsState().unread[channelId] ?? 0;
  const label = unreadLabel(unread);
  const color = unread > 0 ? colors.error : colors.textDim;
  return (
    <button
      data-testid="notifications-button"
      title={unread > 0 ? `Notifications (${unreadTitle(unread)})` : "Notifications"}
      onClick={onToggle}
      style={{
        flexShrink: 0,
        background: open ? colors.selectedBg : "none",
        border: `1px solid ${unread > 0 ? colors.error : colors.border}`,
        color,
        cursor: "pointer",
        padding: "2px 8px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 10,
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
        <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
      </svg>
      {label && <span data-testid="notifications-unread">{label}</span>}
    </button>
  );
}

interface NotificationDrawerProps {
  open: boolean;
  channelId: string;
  channelName: string;
  dirPath: string;
  onClose: () => void;
}

/** How long the drawer takes to slide in or out. */
const DRAWER_MS = 220;

/** A channel's notification drawer: what Loop and the plugins told the
 *  owner about it, newest first. It slides in over the right side of the
 *  layout, dimming the panes behind it, and stays mounted while it slides
 *  out. */
export function NotificationDrawer({ open, channelId, channelName, dirPath, onClose }: NotificationDrawerProps) {
  const { colors } = useTheme();
  const ms = prefersReducedMotion() ? 0 : DRAWER_MS;
  const { mounted, shown } = usePresence(open, ms);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!mounted) return null;
  const ease = `${ms}ms cubic-bezier(0.32, 0.72, 0, 1)`;
  return (
    <>
      <div
        data-testid="notifications-backdrop"
        onClick={onClose}
        style={{
          position: "absolute",
          inset: 0,
          zIndex: 40,
          backgroundColor: "rgba(0,0,0,0.35)",
          opacity: shown ? 1 : 0,
          transition: `opacity ${ease}`,
          pointerEvents: open ? "auto" : "none",
        }}
      />
      <aside
        data-testid="notifications-drawer"
        style={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: 480,
          maxWidth: "100%",
          zIndex: 41,
          display: "flex",
          flexDirection: "column",
          backgroundColor: colors.bg,
          border: colors.islandBorder || `1px solid ${colors.border}`,
          borderRadius: colors.islandRadius,
          boxShadow: shown ? "0 8px 32px rgba(0,0,0,0.35)" : "none",
          overflow: "hidden",
          transform: shown ? "translateX(0)" : "translateX(calc(100% + 16px))",
          transition: `transform ${ease}, box-shadow ${ease}`,
        }}
      >
        <DrawerContent channelId={channelId} channelName={channelName} dirPath={dirPath} onClose={onClose} />
      </aside>
    </>
  );
}

/** The drawer's header and list. Mounting it marks the channel's
 *  notifications read; those unread when it mounted keep a dot. */
function DrawerContent({ channelId, channelName, dirPath, onClose }: Omit<NotificationDrawerProps, "open">) {
  const { colors } = useTheme();
  const { epoch, pluginsEpoch } = useNotificationsState();
  const [notifications, setNotifications] = useState<NotificationInfo[] | null>(null);
  const [fresh, setFresh] = useState<Set<number> | null>(null);
  useEffect(() => {
    let cancelled = false;
    fetchNotifications(channelId)
      .then((l) => {
        if (cancelled) return;
        setNotifications(l.notifications);
        setFresh((f) => f ?? new Set(l.notifications.filter((n) => !n.read).map((n) => n.id)));
      })
      .catch(logErr("listing notifications"));
    return () => {
      cancelled = true;
    };
  }, [channelId, epoch]);

  const list = notifications ?? [];
  const newest = newestId(list);
  const unread = list.some((n) => !n.read);
  useEffect(() => {
    if (unread && newest > 0) markNotificationsRead(channelId, newest).catch(logErr("marking notifications read"));
  }, [channelId, unread, newest]);

  const { channel: cardPlugins, all: allPlugins } = useChannelPlugins(channelId, pluginsEpoch);
  const btn = buildBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "2px 8px", height: 22, flexShrink: 0, backgroundColor: colors.surface, borderBottom: `1px solid ${colors.border}` }}>
        <span style={{ padding: "1px 4px", borderRadius: 3, fontSize: 10, fontWeight: 500, color: colors.textLight, backgroundColor: colors.panelLabelBg }}>Notifications</span>
        <div style={{ flex: 1 }} />
        {list.length > 0 && (
          <button
            data-testid="notifications-clear"
            title="Dismiss all of this channel's notifications"
            onClick={() => clearNotifications(channelId).catch(logErr("clearing notifications"))}
            style={{ ...btn, fontSize: 10, padding: "1px 4px" }}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            Clear all
          </button>
        )}
        <button data-testid="notifications-close" title="Close" onClick={onClose} style={btn} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
          <CloseIcon />
        </button>
      </div>
      <div style={{ flex: 1, overflowY: "auto" }}>
        {notifications && list.length === 0 && <div style={{ padding: 24, textAlign: "center", fontSize: 12, color: colors.textDim }}>Nothing yet.</div>}
        {list.map((n) => {
          const heading = notificationHeading(n);
          return (
            <div key={n.id} data-testid="notification" data-kind={n.kind} style={{ padding: "10px 12px", borderBottom: `1px solid ${colors.border}` }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: colors.textDim }}>
                {fresh?.has(n.id) && <span style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: colors.error, flexShrink: 0 }} />}
                <span style={{ fontWeight: 600, color: colors.textMuted }}>{notificationSource(n, allPlugins)}</span>
                <span style={{ marginLeft: "auto", flexShrink: 0 }}>{timeAgo(n.created_at)}</span>
                <button
                  data-testid="notification-dismiss"
                  title="Dismiss"
                  onClick={() => deleteNotification(n.id).catch(logErr("dismissing a notification"))}
                  style={btn}
                  onMouseEnter={hoverIn}
                  onMouseLeave={hoverOut}
                >
                  <CloseIcon />
                </button>
              </div>
              {heading && <div style={{ marginTop: 4, fontSize: 13, color: colors.text, wordBreak: "break-word" }}>{heading}</div>}
              {n.body && <div style={{ marginTop: 2, fontSize: 12, color: colors.textMuted, whiteSpace: "pre-wrap", wordBreak: "break-word", maxHeight: 120, overflow: "hidden" }}>{n.body}</div>}
              {n.kind === "card" && n.plugin_id && n.card && (
                <PluginCard pluginId={n.plugin_id} cardId={n.card} label={heading} data={n.data ?? null} channelId={channelId} channelName={channelName} dirPath={dirPath} plugins={cardPlugins} />
              )}
            </div>
          );
        })}
      </div>
    </>
  );
}

function CloseIcon() {
  return (
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}

/** The installed plugins as the channel's project sees them, for its cards,
 *  and as installed, for their names; null until they're listed. They're
 *  listed again when the plugins change. */
function useChannelPlugins(channelId: string, epoch: number): { channel: PluginInfo[] | null; all: PluginInfo[] | null } {
  const [plugins, setPlugins] = useState<{ channel: PluginInfo[] | null; all: PluginInfo[] | null }>({ channel: null, all: null });
  const [local, setLocal] = useState(0);
  const latest = useRef(0);
  useEffect(() => {
    const bump = () => setLocal((n) => n + 1);
    window.addEventListener(PLUGINS_CHANGED, bump);
    return () => window.removeEventListener(PLUGINS_CHANGED, bump);
  }, []);
  useEffect(() => {
    const run = ++latest.current;
    const list = (id: string | null) =>
      fetchPlugins(id).catch((err) => {
        logErr("listing plugins")(err);
        return [] as PluginInfo[];
      });
    Promise.all([list(channelId), list(null)]).then(([channel, all]) => {
      if (run === latest.current) setPlugins({ channel, all });
    });
  }, [channelId, epoch, local]);
  return plugins;
}
