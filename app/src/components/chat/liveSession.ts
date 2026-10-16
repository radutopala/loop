import type { LiveStatus } from "../../api/live";
import type { AgentActivityData, BackgroundTask, WSEvent } from "../../types";

/** The live.status event a channel's live session sends when it starts or ends. */
export const LIVE_STATUS_EVENT = "live.status";

/** applyLiveEvent returns channelId's live session after a WS event: a
 *  live.status event replaces it, keeping whether live is enabled (the event
 *  leaves that out); a background_tasks activity sets the tasks it waits on.
 *  Other events, and other channels', leave it as it is. */
export function applyLiveEvent(status: LiveStatus | null, event: WSEvent, channelId: string): LiveStatus | null {
  if (event.channel_id !== channelId) return status;
  if (event.type === LIVE_STATUS_EVENT) {
    const next = event.data as LiveStatus;
    return status?.enabled === undefined || next.enabled !== undefined ? next : { ...next, enabled: status.enabled };
  }
  if (event.type === "agent.activity" && status?.running) {
    const data = event.data as AgentActivityData;
    if (data.activity !== "background_tasks") return status;
    return { ...status, background: data.description || undefined, background_tasks: data.tasks?.length ? data.tasks : undefined };
  }
  return status;
}

// Why a live session ended, as the backend's live.status reasons say it.
const REASONS: Record<string, string> = {
  idle: "stopped after being idle",
  "config changed": "restarting with the new config",
  "scheduled run": "stopped for a scheduled task",
  stopped: "stopped",
  shutdown: "stopped when Loop shut down",
  "stdin closed": "its input closed",
  "interrupt timed out": "it didn't stop when interrupted",
  exited: "it exited",
  "session changed": "the chat moved to another session",
  "one-shot run": "stopped for a one-shot run",
  closed: "its pane closed",
  deleted: "its channel was deleted",
};

/** endedReason describes why a live session ended, for the reasons that
 *  carry a detail ("exited: …", "restart failed: …") too. */
export function endedReason(reason: string): string {
  if (REASONS[reason]) return REASONS[reason];
  const [head, ...rest] = reason.split(": ");
  const detail = rest.join(": ");
  if (head === "exited" && detail) return `it exited: ${detail}`;
  if (head === "restart failed" && detail) return `it couldn't restart: ${detail}`;
  return reason;
}

/** ROUTINE_ENDS are the reasons a session ends that need no explaining: the
 *  dimmed icon already says none is running. */
const ROUTINE_ENDS = new Set(["idle", "stopped"]);

/** liveSummary is what the bar above the composer says when the live
 *  session ended: why. Null when there's nothing to say: a session that
 *  runs (the icon shows whether it's working or idle), none, or one that
 *  ended routinely (idle or stopped). */
export function liveSummary(status: LiveStatus | null): string | null {
  if (!status || status.running) return null;
  return status.reason && !ROUTINE_ENDS.has(status.reason) ? `Session ended: ${endedReason(status.reason)}` : null;
}

/** liveBackground is the background tasks a running live session waits on;
 *  null when it waits on none. */
export function liveBackground(status: LiveStatus | null): BackgroundTask[] | null {
  return (status?.running && status.background_tasks?.length && status.background_tasks) || null;
}

/** shownBackground is the background tasks above the composer: a running
 *  live session's, which can be stopped, or else a running one-shot run's. */
export function shownBackground(status: LiveStatus | null, runTasks: BackgroundTask[] | null, isRunning: boolean): { tasks: BackgroundTask[]; stoppable: boolean } | null {
  if (status?.running) {
    const tasks = liveBackground(status);
    return tasks && { tasks, stoppable: true };
  }
  return isRunning && runTasks?.length ? { tasks: runTasks, stoppable: false } : null;
}

// What each CLI task type is called on its pill.
const TASK_TYPES: Record<string, string> = {
  local_bash: "Shell",
  local_agent: "Agent",
  remote_agent: "Remote agent",
  local_workflow: "Workflow",
  monitor_mcp: "Monitor",
  monitor_ws: "Monitor",
  in_process_teammate: "Teammate",
};

/** taskTypeLabel names a background task's kind. */
export function taskTypeLabel(type: string | undefined): string {
  return (type && TASK_TYPES[type]) || "Background";
}

/** LiveIcon is the footer icon's state: working on a turn (busy: the chat's
 *  run, which follows each turn, unlike the status's busy, a snapshot), a
 *  live session idle between turns, or no session running. */
export type LiveIcon = "working" | "idle" | "off";

export function liveIcon(status: LiveStatus | null, busy: boolean): LiveIcon {
  if (busy) return "working";
  return status?.running ? "idle" : "off";
}

/** liveIconTitle is the footer icon's tooltip. */
export function liveIconTitle(icon: LiveIcon): string {
  if (icon === "working") return "Working";
  return icon === "idle" ? "Live session idle" : "No live session running";
}

/** footerLabel is the chat's footer: whether its turns run in a live
 *  session (one that's running, or starts with the next turn) or one-shot.
 *  Until the status is known it says live, the default, so the footer
 *  doesn't change under the user as the status loads. */
export function footerLabel(enabled: boolean | undefined): string {
  enabled ??= true;
  return enabled ? "Running interactively in an isolated Docker container" : "Running non-interactively in an isolated Docker container";
}

/** shouldWarm reports whether typing in the channel should start its live
 *  session: its turns run live, none runs yet, and none was asked for since
 *  the last one ran. */
export function shouldWarm(status: LiveStatus | null, asked: boolean): boolean {
  return status !== null && status.enabled !== false && !status.running && !asked;
}
