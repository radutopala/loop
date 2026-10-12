import type { NotificationInfo } from "../api/notifications";
import type { PluginInfo } from "../api/plugins";

/** Who a notification is from: Loop, or the plugin by name. */
export function notificationSource(n: NotificationInfo, plugins: PluginInfo[] | null | undefined): string {
  if (!n.plugin_id) return "Loop";
  return plugins?.find((p) => p.id === n.plugin_id)?.name || n.plugin_id;
}

/** A notification's heading: its title, or for a card without one the
 *  card's id. */
export function notificationHeading(n: NotificationInfo): string {
  if (n.title) return n.title;
  return n.kind === "card" && n.card ? n.card : "";
}

/** The newest notification's id, which opening a channel's drawer marks
 *  read up to; 0 for none. */
export function newestId(list: NotificationInfo[]): number {
  return list.reduce((max, n) => Math.max(max, n.id), 0);
}

/** An unread count as the bell and the sidebar show it. */
export function unreadLabel(unread: number): string {
  if (unread <= 0) return "";
  return unread > 99 ? "99+" : String(unread);
}

/** An unread count in words, for a tooltip. */
export function unreadTitle(unread: number): string {
  return unread === 1 ? "1 unread notification" : `${unread} unread notifications`;
}
