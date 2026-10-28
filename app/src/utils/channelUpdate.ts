import type { CIStatus } from "../api/ci";
import type { Channel, ChannelUpdatedData, PluginBadge } from "../types";

/**
 * Applies a channel.updated event to a channel. The branch poller's events
 * carry the git state; a rename, a description or a ticket change carries only
 * its name, description or ticket URL (the git fields come through empty), so those leave the git
 * state as it was — the poller only sends again when it changes.
 */
export function applyChannelUpdate(c: Channel, d: ChannelUpdatedData): Channel {
  if (d.name !== undefined || d.description !== undefined || d.ticket_url !== undefined) {
    return {
      ...c,
      ...(d.name !== undefined ? { name: d.name } : {}),
      ...(d.description !== undefined ? { description: d.description } : {}),
      ...(d.ticket_url !== undefined ? { ticket_url: d.ticket_url } : {}),
    };
  }
  return {
    ...c,
    branch: d.branch,
    commit: d.commit,
    diff_additions: d.diff_additions,
    diff_deletions: d.diff_deletions,
    subject: d.subject,
    upstream: d.upstream,
    ahead: d.ahead,
    behind: d.behind,
    sync_base: d.sync_base,
    base_ahead: d.base_ahead,
    base_behind: d.base_behind,
  };
}

/**
 * Applies a review.status event to a channel, as the channel list would
 * report it on its next fetch: a reviewing or verifying session marks the
 * channel's review running, and the change counts as the channel's activity.
 */
export function applyReviewStatus(c: Channel, status: string, at: number): Channel {
  return { ...c, review_running: status === "reviewing" || status === "verifying", last_activity_at: Math.max(c.last_activity_at ?? 0, at) };
}

/**
 * Applies a plugin.badge event to a channel: the plugin's badge replaces the
 * one it set before, and an empty text clears it. Badges stay in plugin id
 * order, as the channel list returns them.
 */
export function applyPluginBadge(c: Channel, b: PluginBadge): Channel {
  const rest = (c.plugin_badges ?? []).filter((x) => x.plugin_id !== b.plugin_id);
  const badges = b.text ? [...rest, b].sort((x, y) => x.plugin_id.localeCompare(y.plugin_id)) : rest;
  return { ...c, plugin_badges: badges.length > 0 ? badges : undefined };
}

/** A live change to one channel, stamped with performance.now() on arrival. */
export interface ChannelPatch {
  at: number;
  id: string;
  apply: (c: Channel) => Channel;
}

/**
 * Lays live channel patches over a fetched channel list. A fetch's snapshot
 * predates every patch that arrived after the fetch started, so those are
 * applied on top of it (in arrival order) instead of being lost until the
 * next poll. The rest are already in the snapshot; only the newer ones are
 * returned as still pending.
 */
export function replayChannelPatches(channels: Channel[], patches: ChannelPatch[], fetchStartedAt: number): { channels: Channel[]; pending: ChannelPatch[] } {
  const pending = patches.filter((p) => p.at > fetchStartedAt);
  if (pending.length === 0) return { channels, pending };
  return {
    channels: channels.map((c) => pending.reduce((acc, p) => (p.id === c.id ? p.apply(acc) : acc), c)),
    pending,
  };
}

/** Applies a channel.ci event to a channel: its CI attention, unset when none. */
export function applyCI(c: Channel, d: Pick<CIStatus, "ci_attention">): Channel {
  return { ...c, ci_attention: d.ci_attention || undefined };
}
