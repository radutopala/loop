import { unifiedMergeView } from "@codemirror/merge";
import type { Extension } from "@codemirror/state";

/** What the document is compared with: the file on disk, or the file as
 * committed at HEAD. */
export type CompareSource = "disk" | "head";

const LABELS: Record<CompareSource, Record<"reject" | "accept", string>> = {
  disk: { reject: "Use disk", accept: "Keep mine" },
  head: { reject: "Revert", accept: "Keep" },
};

/** One button of a compared chunk: "accept" keeps the editor's text, "reject" takes the original's. */
export function compareChunkButton(type: "reject" | "accept", action: (e: MouseEvent) => void, source: CompareSource = "disk"): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.name = type;
  button.textContent = LABELS[source][type];
  button.dataset.testid = `compare-${type}`;
  button.onmousedown = action;
  return button;
}

/** The inline diff of the document against original, or nothing when not comparing. */
export function compareExtension(original: string | null | undefined, source: CompareSource = "disk"): Extension {
  if (original == null) return [];
  return unifiedMergeView({ original, mergeControls: (type, action) => compareChunkButton(type, action, source), syntaxHighlightDeletions: true });
}

/** The smallest change turning current into next: the span between their
 * common start and end, so what's around it (the cursor) stays put. */
export function minimalChange(current: string, next: string): { from: number; to: number; insert: string } | null {
  if (current === next) return null;
  let start = 0;
  const max = Math.min(current.length, next.length);
  while (start < max && current.charCodeAt(start) === next.charCodeAt(start)) start++;
  let end = 0;
  while (end < max - start && current.charCodeAt(current.length - 1 - end) === next.charCodeAt(next.length - 1 - end)) end++;
  return { from: start, to: current.length - end, insert: next.slice(start, next.length - end) };
}
