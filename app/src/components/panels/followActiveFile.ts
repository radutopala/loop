import type { EditorConfig, EditorConfigSource } from "../../api/loopApi";

/** Where follow active file's value comes from: a config layer, or the
 * channel's own toggle. */
export type FollowSource = EditorConfigSource | "channel";

/** The localStorage key of a channel's follow active file toggle. */
export function followActiveFileKey(channelId: string): string {
  return `loop.editor.followActive.${channelId}`;
}

/** Whether the tree follows the active tab: the channel's toggle ("1" or
 * "0") when set, the config's value otherwise. */
export function resolveFollow(config: EditorConfig, stored: string | null): { on: boolean; source: FollowSource } {
  if (stored === "1" || stored === "0") return { on: stored === "1", source: "channel" };
  return { on: config.follow_active_file, source: config.follow_active_file_from };
}

/** What to store when the toggle flips to on: nothing (null) when that's
 * the config's value, so the channel follows the config again. */
export function storedFollow(config: EditorConfig, on: boolean): string | null {
  return on === config.follow_active_file ? null : on ? "1" : "0";
}

const SOURCE_LABEL: Record<EditorConfigSource, string> = {
  default: "the default",
  global: "the global config",
  project: "the project config",
};

/** The toggle's tooltip: its state, and where that comes from. */
export function followTooltip(config: EditorConfig, follow: { on: boolean; source: FollowSource }): string {
  const what = follow.on ? "The tree opens and scrolls to the file of the active tab" : "The tree stays where it is as tabs change";
  const from =
    follow.source === "channel"
      ? `Set here, for this channel only; ${SOURCE_LABEL[config.follow_active_file_from]} (editor.follow_active_file) has it ${config.follow_active_file ? "on" : "off"}`
      : `From ${SOURCE_LABEL[follow.source]} (editor.follow_active_file); a click sets it for this channel only`;
  return `Follow active file: ${follow.on ? "on" : "off"}\n${what}\n${from}`;
}
