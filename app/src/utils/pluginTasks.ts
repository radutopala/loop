import type { PluginInfo, PluginList, PluginSchedule } from "../api/plugins";
import type { TaskType } from "../components/panels/TaskScheduleField";
import { defaultScheduleForType } from "./taskUtils";

/** The actions of p a config can ask a scheduled task of: each with a
 *  default_schedule, and each a config already asks for. */
export function scheduledActions(p: PluginInfo): string[] {
  if (!p.capabilities.includes("schedule")) return [];
  const named = new Set([...Object.keys(p.default_schedules ?? {}), ...Object.keys(p.global_schedules ?? {}), ...Object.keys(p.schedules ?? {}), ...Object.keys(p.project_schedules ?? {})]);
  return (p.actions ?? []).filter((a) => named.has(a));
}

/** What channelId's project config, or the global config without one, says
 *  of the scheduled task of a plugin action. */
export interface ScheduleState {
  /** The config's own entry. */
  own?: PluginSchedule;
  /** Whether the task is on: in a project, by the entry that applies. */
  on: boolean;
  /** The entry an edit starts from: the own one, else the global one a
   *  project follows, else a new one, off until turned on. */
  base: PluginSchedule;
  /** The schedule an entry without one runs on. */
  fallback?: { type?: string; schedule?: string };
  /** Whether the project's own entry waits for the owner's trust. */
  pending: boolean;
}

/** What channelId's project config, or the global config without one, says
 *  of the scheduled task of p's action a. */
export function scheduleState(p: PluginInfo, a: string, channelId?: string | null): ScheduleState {
  const own = channelId ? p.project_schedules?.[a] : p.global_schedules?.[a];
  const below = channelId ? p.global_schedules?.[a] : undefined;
  const want = channelId ? p.schedules?.[a] : own;
  return {
    own,
    on: want !== undefined && want.enabled !== false,
    base: own ?? below ?? { enabled: false },
    fallback: below?.schedule ? below : p.default_schedules?.[a],
    pending: !!channelId && own !== undefined && JSON.stringify(own) !== JSON.stringify(want),
  };
}

/** A plugin action the Tasks panel can make a task of. */
export interface PluginTaskOption {
  key: string;
  label: string;
  plugin: PluginInfo;
  action: string;
  /** The type and schedule the task starts on: what the project's config
   *  asks for, else the action's default. */
  type: TaskType;
  schedule: string;
  /** Whether a config already asks for the task, on. */
  scheduled: boolean;
  state: ScheduleState;
}

/** The actions of the plugins on in channelId's project a task can be
 *  made of. */
export function pluginTaskOptions(list: PluginList | null, channelId: string): PluginTaskOption[] {
  if (!list || list.off) return [];
  return list.plugins
    .filter((p) => p.enabled)
    .flatMap((p) =>
      scheduledActions(p).map((action) => {
        const state = scheduleState(p, action, channelId);
        const from = state.own?.schedule ? state.own : state.fallback;
        const type: TaskType = from?.type === "interval" ? "interval" : "cron";
        return {
          key: `${p.id}/${action}`,
          label: `${p.name || p.id}: ${action}`,
          plugin: p,
          action,
          type,
          schedule: from?.schedule || defaultScheduleForType(type),
          scheduled: state.on,
          state,
        };
      }),
    );
}

/** The project config entry asking for o's task at type and schedule, on
 *  unless enabled is false; the type and schedule are left out when they're
 *  the default one the entry falls back to. */
export function pluginTaskEntry(o: PluginTaskOption, type: TaskType, schedule: string, enabled = true): PluginSchedule {
  const fb = o.state.fallback;
  const isDefault = !!fb?.schedule && fb.schedule === schedule.trim() && (fb.type || "cron") === type;
  return {
    ...o.state.base,
    enabled,
    type: isDefault || type === "cron" ? undefined : type,
    schedule: isDefault ? undefined : schedule.trim(),
  };
}

/** How a plugin action's task runs, which its config entry doesn't hold: in
 *  a worktree thread, updated to originBranch first when update is set, and
 *  deleted autoDeleteSec after the action started. Undefined without a
 *  worktree, the only kind of plugin task with a thread to delete. */
export function pluginRunOptions(
  worktree: boolean,
  originBranch: string,
  update: boolean,
  autoDeleteSec: number,
): { worktree: boolean; origin_branch: string; update_before_run: boolean; auto_delete_sec: number } | undefined {
  if (!worktree) return undefined;
  return { worktree, origin_branch: originBranch.trim(), update_before_run: update, auto_delete_sec: autoDeleteSec };
}
