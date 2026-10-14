import { useEffect, useState } from "react";
import type { Monitor } from "../../api/monitors";
import { cancelMonitor, fetchMonitors } from "../../api/monitors";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";
import { elapsed } from "../../utils/workflowHelpers";
import { applyMonitorEvent } from "./monitorEvents";

interface MonitorsBarProps {
  channelId: string;
  subscribeChatEvents: (listener: ChatEventListener) => () => void;
  /** Bumped on each WS (re)connect, so events missed while it was down are caught up. */
  wsOpens?: number;
}

/**
 * MonitorsBar lists the watch scripts the channel's agent left running with
 * the monitor MCP tool, each with how long it has run and a button to cancel
 * it. A monitor that ends drops out; the message it wakes the agent with
 * shows in the chat.
 */
export function MonitorsBar({ channelId, subscribeChatEvents, wsOpens }: MonitorsBarProps) {
  const { colors } = useTheme();
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [cancelling, setCancelling] = useState<Set<string>>(new Set());
  const [, setTick] = useState(0);

  useEffect(() => {
    let live = true;
    setMonitors([]);
    fetchMonitors(channelId)
      .then((list) => live && setMonitors(list))
      .catch(logErr("fetching monitors"));
    return () => {
      live = false;
    };
  }, [channelId, wsOpens]);

  useEffect(() => subscribeChatEvents((event) => setMonitors((list) => applyMonitorEvent(list, event, channelId))), [subscribeChatEvents, channelId]);

  // Keeps the elapsed times current.
  const running = monitors.length > 0;
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, [running]);

  if (!running) return null;

  const cancel = (id: string) => {
    setCancelling((prev) => new Set(prev).add(id));
    cancelMonitor(channelId, id)
      .catch(logErr("cancelling monitor"))
      .finally(() =>
        setCancelling((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        }),
      );
  };

  return (
    <div data-testid="monitors-bar" style={{ display: "flex", flexDirection: "column", gap: 4, padding: "6px 24px 0" }}>
      {monitors.map((m) => (
        <div
          key={m.id}
          data-testid="monitor-row"
          title={m.command}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            padding: "4px 10px",
            border: `1px solid ${colors.border}`,
            borderRadius: 6,
            background: colors.surface,
            fontSize: 12,
            color: colors.textMuted,
          }}
        >
          <span style={{ color: colors.textDim }}>Monitoring</span>
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: colors.text }}>{m.description}</span>
          <span style={{ fontFamily: fonts.mono, color: colors.textDim }}>{elapsed(m.started_at, null)}</span>
          <button
            data-testid="monitor-cancel"
            onClick={() => cancel(m.id)}
            disabled={cancelling.has(m.id)}
            title="Stop this monitor without waking the agent"
            style={{
              padding: "1px 8px",
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              background: "transparent",
              color: colors.textMuted,
              fontSize: 11,
              cursor: cancelling.has(m.id) ? "default" : "pointer",
            }}
          >
            Cancel
          </button>
        </div>
      ))}
    </div>
  );
}
