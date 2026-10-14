import type { Monitor, MonitorFinished } from "../../api/monitors";
import type { WSEvent } from "../../types";

/**
 * applyMonitorEvent returns channelId's monitor list after a WS event:
 * monitor.started adds one, monitor.finished drops it. Other events, and
 * other channels' monitors, leave the list as it is (the same array).
 */
export function applyMonitorEvent(list: Monitor[], event: WSEvent, channelId: string): Monitor[] {
  if (event.channel_id !== channelId) return list;
  switch (event.type) {
    case "monitor.started": {
      const mon = event.data as Monitor;
      if (list.some((m) => m.id === mon.id)) return list;
      return [...list, mon];
    }
    case "monitor.finished": {
      const { id } = event.data as MonitorFinished;
      return list.some((m) => m.id === id) ? list.filter((m) => m.id !== id) : list;
    }
    default:
      return list;
  }
}
