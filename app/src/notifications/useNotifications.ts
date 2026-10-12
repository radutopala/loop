import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { fetchUnreadNotifications } from "../api/notifications";
import { logErr } from "../utils/log";

/** What a channel's bell, drawer and sidebar row read about the
 *  notifications. */
export interface NotificationsState {
  /** How many unread notifications each channel has. */
  unread: Record<string, number>;
  /** Bumped when the notifications may have changed, for a drawer to list
   *  its channel's again. */
  epoch: number;
  /** Bumped when the installed plugins change. */
  pluginsEpoch: number;
}

export const NotificationsContext = createContext<NotificationsState>({ unread: {}, epoch: 0, pluginsEpoch: 0 });

export function useNotificationsState(): NotificationsState {
  return useContext(NotificationsContext);
}

/** How many unread notifications each channel has, with refetch to call
 *  when the server says they changed. They're also fetched every 30
 *  seconds, for an event missed while the connection was down. */
export function useUnreadNotifications(ready: boolean): { unread: Record<string, number>; epoch: number; refetch: () => void } {
  const [unread, setUnread] = useState<Record<string, number>>({});
  const [epoch, setEpoch] = useState(0);
  const refetch = useCallback(() => setEpoch((n) => n + 1), []);
  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    fetchUnreadNotifications()
      .then((u) => {
        if (!cancelled) setUnread(u);
      })
      .catch(logErr("counting unread notifications"));
    return () => {
      cancelled = true;
    };
  }, [ready, epoch]);
  useEffect(() => {
    const id = setInterval(refetch, 30_000);
    return () => clearInterval(id);
  }, [refetch]);
  return { unread, epoch, refetch };
}
