export const EFFORT_PRESETS = ["low", "medium", "high", "xhigh", "max"];

/** Strip the common "claude-" prefix so labels stay compact. */
export function shortModel(model: string): string {
  return model.replace(/^claude-/, "");
}
