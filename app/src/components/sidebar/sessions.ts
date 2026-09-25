import type { Channel } from "../../types";

// The sidebar's Recent section lists sessions (channels and threads alike)
// above the channel tree, newest first, so a running agent or one waiting on
// the user is visible however deep it sits in the tree: an active session
// counts as active now, so it tops the list.

/** How far back Recent looks. */
export const RECENT_WINDOW_MS = 24 * 60 * 60 * 1000;
/** Recent rows shown at first, and added by each "show more". */
export const RECENT_LIMIT = 10;

const TASK_PREFIX = /^(\[ephemeral] )?(🧵 |⏱ )?/;

/** Whether a thread is one a scheduled task created for its output. */
export function isTaskThread(channel: Channel): boolean {
  return !!channel.task_id;
}

/** A session's name as the sidebar shows it: task threads lose their marker prefix. */
export function sessionName(channel: Channel): string {
  if (/^(\[ephemeral] )?(🧵 |⏱ )?task #/.test(channel.name)) return channel.name.replace(TASK_PREFIX, "");
  return channel.name || channel.dir_path?.split("/").pop() || channel.id;
}

/** Where a thread lives: its ancestors' names, outermost first. Empty for a top-level channel. */
export function sessionContext(channel: Channel, byId: Map<string, Channel>): string {
  const names: string[] = [];
  const seen = new Set<string>([channel.id]);
  let parent = byId.get(channel.parent_id);
  while (parent && !seen.has(parent.id)) {
    seen.add(parent.id);
    names.unshift(sessionName(parent));
    parent = byId.get(parent.parent_id);
  }
  return names.join(" › ");
}

/** Channels the tree shows at all: top-level ones need a name or a dir. */
function listed(channel: Channel): boolean {
  return !!channel.parent_id || !!channel.name || !!channel.dir_path;
}

/**
 * The Active section: sessions waiting on the user (an approval, a question,
 * a plan, a ready review), then those with an agent running. A container
 * that's only idling doesn't count: nothing is happening in it.
 */
export function activeSessions(channels: Channel[], isRunning: (id: string) => boolean, needsYou: (id: string) => boolean): Channel[] {
  const waiting: Channel[] = [];
  const running: Channel[] = [];
  for (const c of channels) {
    if (!listed(c)) continue;
    if (needsYou(c.id)) waiting.push(c);
    else if (isRunning(c.id)) running.push(c);
  }
  return [...waiting, ...running];
}

/**
 * The Recent section: sessions active within the window, newest first.
 */
export function recentSessions(channels: Channel[], lastActivity: (channel: Channel) => number | undefined, now: number, windowMs = RECENT_WINDOW_MS): Channel[] {
  return channels
    .filter(listed)
    .map((c) => ({ c, at: lastActivity(c) ?? 0 }))
    .filter(({ at }) => at > 0 && now - at <= windowMs)
    .sort((a, b) => b.at - a.at)
    .map(({ c }) => c);
}

/** A compact age: "now", "5m", "3h", "2d". */
export function relativeTime(ms: number): string {
  const min = Math.floor(Math.max(0, ms) / 60_000);
  if (min < 1) return "now";
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}
