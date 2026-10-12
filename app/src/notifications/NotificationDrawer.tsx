import { useEffect, useRef, useState } from "react";
import { clearNotifications, deleteNotification, markNotificationsRead, type NotificationInfo } from "../api/notifications";
import { fetchPlugins, PLUGINS_CHANGED, type PluginInfo } from "../api/plugins";
import { PluginCard } from "../plugins/PluginCard";
import { useTheme } from "../ThemeContext";
import type { Channel } from "../types";
import { logErr } from "../utils/log";
import { timeAgo } from "../utils/workflowHelpers";
import { cardChannels, channelLabel, newestId, notificationHeading, notificationSource, unreadLabel } from "./notifications";

/** The header button that opens and closes the drawer, with the unread
 *  count. */
export function NotificationButton({ unread, open, onToggle }: { unread: number; open: boolean; onToggle: () => void }) {
  const { colors } = useTheme();
  const label = unreadLabel(unread);
  return (
    <button
      data-testid="notifications-button"
      title={unread > 0 ? `Notifications (${unread} unread)` : "Notifications"}
      onClick={onToggle}
      style={{
        position: "absolute",
        top: 7,
        right: 72,
        zIndex: 11,
        display: "flex",
        alignItems: "center",
        gap: 4,
        height: 22,
        padding: "0 6px",
        border: "none",
        borderRadius: 6,
        backgroundColor: open ? colors.selectedBg : "transparent",
        color: unread > 0 ? colors.text : colors.textDim,
        cursor: "pointer",
        fontFamily: "inherit",
        WebkitAppRegion: "no-drag",
      }}
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
        <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
      </svg>
      {label && (
        <span
          data-testid="notifications-unread"
          style={{ minWidth: 14, padding: "0 4px", borderRadius: 7, backgroundColor: colors.error, color: colors.white, fontSize: 10, fontWeight: 600, lineHeight: "14px" }}
        >
          {label}
        </span>
      )}
    </button>
  );
}

interface NotificationDrawerProps {
  notifications: NotificationInfo[];
  unread: number;
  channels: Channel[];
  /** Bumped when the installed plugins change. */
  pluginsEpoch: number;
  onSelectChannel: (id: string) => void;
  onClose: () => void;
}

/** The notification drawer: what Loop and the plugins told the owner,
 *  newest first, over the right side of the window. Opening it marks them
 *  read; those unread when it opened keep a dot while it's open. */
export function NotificationDrawer({ notifications, unread, channels, pluginsEpoch, onSelectChannel, onClose }: NotificationDrawerProps) {
  const { colors } = useTheme();
  const [fresh] = useState(() => new Set(notifications.filter((n) => !n.read).map((n) => n.id)));
  const newest = newestId(notifications);
  useEffect(() => {
    if (unread > 0 && newest > 0) markNotificationsRead(newest).catch(logErr("marking notifications read"));
  }, [unread, newest]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const plugins = useCardPlugins(notifications, pluginsEpoch);
  const allPlugins = plugins[""] ?? null;

  return (
    <>
      <div data-testid="notifications-backdrop" onClick={onClose} style={{ position: "fixed", inset: 0, zIndex: 40, WebkitAppRegion: "no-drag" }} />
      <aside
        data-testid="notifications-drawer"
        style={{
          position: "fixed",
          top: colors.islandGap,
          right: colors.islandGap,
          bottom: colors.islandGap,
          width: 400,
          maxWidth: "calc(100vw - 24px)",
          zIndex: 41,
          display: "flex",
          flexDirection: "column",
          backgroundColor: colors.bg,
          border: colors.islandBorder,
          borderRadius: colors.islandRadius,
          boxShadow: "0 8px 32px rgba(0,0,0,0.35)",
          overflow: "hidden",
          WebkitAppRegion: "no-drag",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "10px 12px", borderBottom: `1px solid ${colors.border}` }}>
          <span style={{ flex: 1, fontSize: 13, fontWeight: 600, color: colors.text }}>Notifications</span>
          {notifications.length > 0 && (
            <button data-testid="notifications-clear" onClick={() => clearNotifications().catch(logErr("clearing notifications"))} style={textButton(colors.textDim)}>
              Clear all
            </button>
          )}
          <button data-testid="notifications-close" title="Close" onClick={onClose} style={textButton(colors.textDim)}>
            ×
          </button>
        </div>
        <div style={{ flex: 1, overflowY: "auto" }}>
          {notifications.length === 0 ? (
            <div style={{ padding: 24, textAlign: "center", fontSize: 12, color: colors.textDim }}>Nothing yet.</div>
          ) : (
            notifications.map((n) => {
              const channel = channels.find((c) => c.id === n.channel_id);
              const where = channelLabel(n.channel_id, channels);
              const cardPlugins = n.channel_id ? (plugins[n.channel_id] ?? null) : null;
              const heading = notificationHeading(n);
              return (
                <div
                  key={n.id}
                  data-testid="notification"
                  data-kind={n.kind}
                  onClick={channel ? () => onSelectChannel(channel.id) : undefined}
                  style={{ padding: "10px 12px", borderBottom: `1px solid ${colors.border}`, cursor: channel ? "pointer" : "default" }}
                >
                  <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: colors.textDim }}>
                    {fresh.has(n.id) && <span style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: colors.error, flexShrink: 0 }} />}
                    <span style={{ fontWeight: 600, color: colors.textMuted }}>{notificationSource(n, allPlugins)}</span>
                    {where && <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>· {where}</span>}
                    <span style={{ marginLeft: "auto", flexShrink: 0 }}>{timeAgo(n.created_at)}</span>
                    <button
                      data-testid="notification-dismiss"
                      title="Dismiss"
                      onClick={(e) => {
                        e.stopPropagation();
                        deleteNotification(n.id).catch(logErr("dismissing a notification"));
                      }}
                      style={textButton(colors.textDim)}
                    >
                      ×
                    </button>
                  </div>
                  {heading && <div style={{ marginTop: 4, fontSize: 13, color: colors.text, wordBreak: "break-word" }}>{heading}</div>}
                  {n.body && <div style={{ marginTop: 2, fontSize: 12, color: colors.textMuted, whiteSpace: "pre-wrap", wordBreak: "break-word", maxHeight: 120, overflow: "hidden" }}>{n.body}</div>}
                  {n.kind === "card" && n.plugin_id && n.card && n.channel_id && (
                    // The card's page handles its own clicks.
                    <div onClick={(e) => e.stopPropagation()}>
                      <PluginCard
                        pluginId={n.plugin_id}
                        cardId={n.card}
                        label={heading}
                        data={n.data ?? null}
                        channelId={n.channel_id}
                        channelName={channel?.name ?? ""}
                        dirPath={channel?.dir_path ?? ""}
                        plugins={cardPlugins}
                      />
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>
      </aside>
    </>
  );
}

function textButton(color: string): React.CSSProperties {
  return { border: "none", background: "transparent", color, fontSize: 12, cursor: "pointer", fontFamily: "inherit", padding: "0 4px" };
}

/** The installed plugins as each card's channel's project sees them, by
 *  channel, and under "" as installed, for their names; null for a channel
 *  until they're listed. They're listed again when the plugins change. */
function useCardPlugins(notifications: NotificationInfo[], epoch: number): Record<string, PluginInfo[] | null> {
  const ids = cardChannels(notifications);
  const key = ids.join("\0");
  const [plugins, setPlugins] = useState<Record<string, PluginInfo[] | null>>({});
  const [local, setLocal] = useState(0);
  const latest = useRef(0);
  useEffect(() => {
    const bump = () => setLocal((n) => n + 1);
    window.addEventListener(PLUGINS_CHANGED, bump);
    return () => window.removeEventListener(PLUGINS_CHANGED, bump);
  }, []);
  useEffect(() => {
    const run = ++latest.current;
    const channelIds = ["", ...(key ? key.split("\0") : [])];
    Promise.all(
      channelIds.map((id) =>
        fetchPlugins(id || null).then(
          (p) => [id, p] as const,
          (err) => {
            logErr("listing plugins")(err);
            return [id, [] as PluginInfo[]] as const;
          },
        ),
      ),
    ).then((entries) => {
      if (run === latest.current) setPlugins(Object.fromEntries(entries));
    });
  }, [key, epoch, local]);
  return plugins;
}
