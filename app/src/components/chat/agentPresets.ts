// Mirrors the config schema's claude_model options (internal/config/schema.go).
// Anything the list drops — a retired id like opus-4-6, or one released after
// this ships — still works through a custom id: it is passed to the Claude CLI
// verbatim, never checked against this list.
export const MODEL_PRESETS = ["claude-opus-5-5", "claude-opus-5", "claude-fable-5-1", "claude-fable-5", "claude-opus-4-8", "claude-sonnet-5"];
export const EFFORT_PRESETS = ["low", "medium", "high", "xhigh", "max"];

/** Strip the common "claude-" prefix so labels stay compact. */
export function shortModel(model: string): string {
  return model.replace(/^claude-/, "");
}
