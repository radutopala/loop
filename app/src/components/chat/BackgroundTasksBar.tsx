import { useEffect, useState } from "react";
import { stopLiveTask } from "../../api/live";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { BackgroundTask } from "../../types";
import { logErr } from "../../utils/log";
import { elapsed } from "../../utils/workflowHelpers";
import { HoverTip } from "./HoverTip";
import { taskTypeLabel } from "./liveSession";
import { tailCommand, tailInShell, tailPlan } from "./tailInShell";

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
 * drops out. Tail follows a task's output in the chat's Shell view, which
 * runs in the container the task writes it in: the files its command
 * redirects its output to, and its own; see tailPlan. A Tail that fails
 * says why in its row.
 */
export function BackgroundTasksBar({ channelId, tasks, stoppable }: BackgroundTasksBarProps) {
  const { colors } = useTheme();
  const [stopping, setStopping] = useState<Set<string>>(new Set());
  // Why the last Tail failed, by task id.
  const [tailErrors, setTailErrors] = useState<Record<string, string>>({});
  const [, setTick] = useState(0);

  // Keeps the elapsed times current.
  useEffect(() => {
    const timer = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, []);

  const rowButton = (busy: boolean) => ({
    padding: "1px 8px",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textMuted,
    fontSize: 11,
    cursor: busy ? "default" : "pointer",
  });

  const tail = (t: BackgroundTask, line: string) => {
    setTailErrors(({ [t.id]: _, ...rest }) => rest);
    tailInShell(channelId, line).catch((err: unknown) => {
      logErr("following the background task's output")(err);
      setTailErrors((prev) => ({ ...prev, [t.id]: err instanceof Error ? err.message : String(err) }));
    });
  };

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
      {tasks.map((t, i) => {
        const plan = tailPlan(t, tasks);
        const tailError = tailErrors[t.id];
        return (
          <div
            key={t.id || i}
            data-testid="background-task-row"
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
            {/* The whole command shows on hover, as the composer's tips do. */}
            <HoverTip text={t.description ? `${taskTypeLabel(t.type)}\n${t.description}` : ""} shrink mono width={420}>
              <span data-testid="background-task-description" style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: colors.text }}>
                {t.description || "Background task"}
              </span>
            </HoverTip>
            <span data-testid="background-task-elapsed" style={{ marginLeft: "auto", fontFamily: fonts.mono, color: colors.textDim }}>
              {elapsed(t.started_at, null)}
            </span>
            {tailError && (
              <span data-testid="background-task-tail-error" title={tailError} style={{ color: colors.error, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>
                Can't tail: {tailError}
              </span>
            )}
            {plan && (
              <button
                data-testid="background-task-tail"
                onClick={() => tail(t, tailCommand(t, plan))}
                title={`Follow its output in the Shell view\n${plan.files.map((f) => f.shown).join("\n")}`}
                style={rowButton(false)}
              >
                Tail
              </button>
            )}
            {stoppable && t.id && (
              <button data-testid="background-task-stop" onClick={() => stop(t.id)} disabled={stopping.has(t.id)} title="Stop this background task" style={rowButton(stopping.has(t.id))}>
                Stop
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
