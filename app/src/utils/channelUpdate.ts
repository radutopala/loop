import type { Channel, ChannelUpdatedData } from "../types";

/**
 * Applies a channel.updated event to a channel. The branch poller's events
 * carry the git state; a rename or a description change carries only its name
 * or description (the git fields come through empty), so those leave the git
 * state as it was — the poller only sends again when it changes.
 */
export function applyChannelUpdate(c: Channel, d: ChannelUpdatedData): Channel {
  if (d.name !== undefined || d.description !== undefined) {
    return {
      ...c,
      ...(d.name !== undefined ? { name: d.name } : {}),
      ...(d.description !== undefined ? { description: d.description } : {}),
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
