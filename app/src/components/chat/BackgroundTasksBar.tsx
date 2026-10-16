import { useEffect, useState } from "react";
import { stopLiveTask } from "../../api/live";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { BackgroundTask } from "../../types";
import { logErr } from "../../utils/log";
import { elapsed } from "../../utils/workflowHelpers";
import { taskTypeLabel } from "./liveSession";

interface BackgroundTasksBarProps {
  channelId: string;
  tasks: BackgroundTask[];
  /** Whether the tasks can be stopped: a live session's can. */
  stoppable: boolean;
}

/**
 * BackgroundTasksBar lists, above the composer, the tasks the channel's
 * claude process runs in the background (shell commands, Claude's Monitor,
 * subagents), as MonitorsBar lists Loop's monitors: each with how long it
 * has run and, in a live session, a button to stop it. A task that ends
 * drops out.
 */
export function BackgroundTasksBar({ channelId, tasks, stoppable }: BackgroundTasksBarProps) {
  const { colors } = useTheme();
  const [stopping, setStopping] = useState<Set<string>>(new Set());
  const [, setTick] = useState(0);

  // Keeps the elapsed times current.
  useEffect(() => {
    const timer = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, []);

  const stop = (id: string) => {
    setStopping((prev) => new Set(prev).add(id));
    stopLiveTask(channelId, id)
      .catch(logErr("stopping the background task"))
      .finally(() =>
        setStopping((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        }),
      );
  };

  return (
    <div data-testid="background-tasks-bar" style={{ display: "flex", flexDirection: "column", gap: 4, padding: "6px 24px 0" }}>
      {tasks.map((t, i) => (
        <div
          key={t.id || i}
          data-testid="background-task-row"
          title={t.description}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            // As wide as the composer under it.
            width: "100%",
            maxWidth: 768,
            margin: "0 auto",
            boxSizing: "border-box",
            padding: "4px 10px",
            border: `1px solid ${colors.border}`,
            borderRadius: 6,
            background: colors.surface,
            fontSize: 12,
            color: colors.textMuted,
          }}
        >
          <span style={{ color: colors.textDim }}>{taskTypeLabel(t.type)}</span>
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: colors.text }}>{t.description || "Background task"}</span>
          <span data-testid="background-task-elapsed" style={{ fontFamily: fonts.mono, color: colors.textDim }}>
            {elapsed(t.started_at, null)}
          </span>
          {stoppable && t.id && (
            <button
              data-testid="background-task-stop"
              onClick={() => stop(t.id)}
              disabled={stopping.has(t.id)}
              title="Stop this background task"
              style={{
                padding: "1px 8px",
                border: `1px solid ${colors.border}`,
                borderRadius: 4,
                background: "transparent",
                color: colors.textMuted,
                fontSize: 11,
                cursor: stopping.has(t.id) ? "default" : "pointer",
              }}
            >
              Stop
            </button>
          )}
        </div>
      ))}
    </div>
  );
}
