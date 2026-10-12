import { useCallback, useEffect, useState } from "react";
import { fetchNotifications, type NotificationList } from "../api/notifications";
import { logErr } from "../utils/log";

/** The notifications and how many are unread, with refetch to call when
 *  the server says they changed. They're also fetched every 30 seconds, for
 *  an event missed while the connection was down. */
export function useNotifications(ready: boolean): NotificationList & { refetch: () => void } {
  const [list, setList] = useState<NotificationList>({ notifications: [], unread: 0 });
  const [epoch, setEpoch] = useState(0);
  const refetch = useCallback(() => setEpoch((n) => n + 1), []);
  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    fetchNotifications()
      .then((l) => {
        if (!cancelled) setList(l);
      })
      .catch(logErr("listing notifications"));
    return () => {
      cancelled = true;
    };
  }, [ready, epoch]);
  useEffect(() => {
    const id = setInterval(refetch, 30_000);
    return () => clearInterval(id);
  }, [refetch]);
  return { ...list, refetch };
}
