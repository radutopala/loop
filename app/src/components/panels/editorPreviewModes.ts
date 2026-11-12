import { storageGetJSON, storageSetJSON } from "../../utils/storage";

export type PreviewMode = "editor" | "both" | "preview";

/** The editor's Markdown and HTML view modes. Markdown opens split, HTML
 * opens rendered. */
export interface PreviewModes {
  md: PreviewMode;
  html: PreviewMode;
}

const KEY = "loop-editor-preview-modes";
const DEFAULTS: PreviewModes = { md: "both", html: "preview" };
const MODES: readonly unknown[] = ["editor", "both", "preview"];

const valid = (m: unknown, fallback: PreviewMode): PreviewMode => (MODES.includes(m) ? (m as PreviewMode) : fallback);

/** The channel's saved modes, so they survive switching channels. */
export function loadPreviewModes(channelId: string): PreviewModes {
  const saved = storageGetJSON<Record<string, Partial<PreviewModes>>>(KEY)?.[channelId];
  return { md: valid(saved?.md, DEFAULTS.md), html: valid(saved?.html, DEFAULTS.html) };
}

export function savePreviewModes(channelId: string, modes: PreviewModes): void {
  const all = storageGetJSON<Record<string, PreviewModes>>(KEY) ?? {};
  if (modes.md === DEFAULTS.md && modes.html === DEFAULTS.html) delete all[channelId];
  else all[channelId] = modes;
  storageSetJSON(KEY, all);
}
