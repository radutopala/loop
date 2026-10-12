import { apiFetch, getApiUrl } from "./api";

/** Something Loop or a plugin told the owner, as the drawer lists it. */
export interface NotificationInfo {
  id: number;
  /** Empty for Loop's own. */
  plugin_id?: string;
  kind: "run" | "workflow" | "notify" | "card" | string;
  /** The channel or thread it's about, if any. */
  channel_id?: string;
  title?: string;
  body?: string;
  /** A card's id under the plugin's ui.cards, and the data it was posted with. */
  card?: string;
  data?: unknown;
  read: boolean;
  created_at: string;
}

export interface NotificationList {
  notifications: NotificationInfo[];
  unread: number;
}

/** The event telling the app the notifications changed. */
export const NOTIFICATIONS_CHANGED = "notifications.changed";

const base = () => `${getApiUrl()}/api/notifications`;

async function check(res: Response, what: string): Promise<void> {
  if (res.ok) return;
  const body = (await res.text().catch(() => "")).trim();
  throw new Error(`Failed to ${what}: ${body || res.statusText}`);
}

/** The newest notifications, newest first; none while they're off. */
export async function fetchNotifications(): Promise<NotificationList> {
  const res = await apiFetch(base());
  if (res.status === 501) return { notifications: [], unread: 0 };
  await check(res, "list notifications");
  return res.json();
}

/** Marks the notifications up to id read. */
export async function markNotificationsRead(upTo: number): Promise<void> {
  const res = await apiFetch(`${base()}/read`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ up_to: upTo }),
  });
  await check(res, "mark notifications read");
}

/** Dismisses a notification; one already gone is fine. */
export async function deleteNotification(id: number): Promise<void> {
  const res = await apiFetch(`${base()}/${id}`, { method: "DELETE" });
  if (res.status === 404) return;
  await check(res, "dismiss the notification");
}

/** Dismisses every notification. */
export async function clearNotifications(): Promise<void> {
  const res = await apiFetch(base(), { method: "DELETE" });
  await check(res, "clear notifications");
}
