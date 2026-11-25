import type { Channel } from "../types";

/** channel builds a sidebar Channel with every required field empty, overridden by fields. */
export function channel(fields: Partial<Channel> = {}): Channel {
  return {
    id: "ch",
    name: "ch",
    parent_id: "",
    dir_path: "",
    session_id: "",
    active: true,
    container_running: false,
    agent_running: false,
    branch: "",
    commit: "",
    worktree: false,
    locked: false,
    diff_additions: 0,
    diff_deletions: 0,
    review_enabled: false,
    ...fields,
  };
}
