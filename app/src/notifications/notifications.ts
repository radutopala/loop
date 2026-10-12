import type { NotificationInfo } from "../api/notifications";
import type { PluginInfo } from "../api/plugins";
import type { Channel } from "../types";

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

/** The channel a notification is about, as "parent › thread" for a thread;
 *  "" when it's about none or the channel is gone. */
export function channelLabel(channelId: string | undefined, channels: Channel[]): string {
  if (!channelId) return "";
  const ch = channels.find((c) => c.id === channelId);
  if (!ch) return "";
  const parent = ch.parent_id ? channels.find((c) => c.id === ch.parent_id) : undefined;
  return parent ? `${parent.name} › ${ch.name}` : ch.name;
}

/** The newest notification's id, which opening the drawer marks read up
 *  to; 0 for none. */
export function newestId(list: NotificationInfo[]): number {
  return list.reduce((max, n) => Math.max(max, n.id), 0);
}

/** The unread count as the header button shows it. */
export function unreadLabel(unread: number): string {
  if (unread <= 0) return "";
  return unread > 99 ? "99+" : String(unread);
}

/** The channels the cards are about, each once, in the order of the list. */
export function cardChannels(list: NotificationInfo[]): string[] {
  const out: string[] = [];
  for (const n of list) {
    if (n.kind === "card" && n.channel_id && !out.includes(n.channel_id)) out.push(n.channel_id);
  }
  return out;
}
