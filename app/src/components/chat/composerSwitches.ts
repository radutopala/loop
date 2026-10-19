import type { ComposerSwitch, ComposerSwitchState } from "../../api/composerSwitches";

/** A switch's key in a chat: its plugin and id. */
export function switchKey(sw: Pick<ComposerSwitch, "plugin_id" | "switch_id">): string {
  return `${sw.plugin_id}/${sw.switch_id}`;
}

/** list with the switch of key showing state; a switch not in list (one
 *  the plugin set before the list was fetched again) is left out. */
export function withSwitchState(list: ComposerSwitch[], key: string, state: ComposerSwitchState): ComposerSwitch[] {
  return list.map((sw) => (switchKey(sw) === key ? { ...sw, on: state.on, status: state.status, title: state.title } : sw));
}

/** How a pill looks: its state, or, while the plugin hasn't answered a
 *  click, the state it was turned to. */
export type SwitchLook = "off" | "on" | "busy" | "error";

export function switchLook(sw: ComposerSwitchState, pending: boolean, failed: boolean): SwitchLook {
  if (failed || sw.status === "error") return "error";
  if (pending || sw.status === "busy") return "busy";
  return sw.on ? "on" : "off";
}

/** A pill's tooltip: the label, then the plugin's title or the manifest's,
 *  then the error of a failed click. */
export function switchTip(sw: ComposerSwitch, error: string | undefined): string {
  const head = sw.label || sw.switch_id;
  const lines = [head];
  const title = sw.title || sw.default_title;
  if (title && title !== head) lines.push(title);
  else if (!title) lines.push(sw.on ? "On" : "Off");
  if (error) lines.push(error);
  return lines.join("\n");
}
