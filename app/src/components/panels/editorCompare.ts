import { unifiedMergeView } from "@codemirror/merge";
import type { Extension } from "@codemirror/state";

/** One button of a compared chunk: "accept" keeps the editor's text, "reject" takes the original's. */
export function compareChunkButton(type: "reject" | "accept", action: (e: MouseEvent) => void): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.name = type;
  button.textContent = type === "reject" ? "Use disk" : "Keep mine";
  button.dataset.testid = `compare-${type}`;
  button.onmousedown = action;
  return button;
}

/** The inline diff of the document against the file on disk, or nothing when not comparing. */
export function compareExtension(original: string | null | undefined): Extension {
  if (original == null) return [];
  return unifiedMergeView({ original, mergeControls: compareChunkButton, syntaxHighlightDeletions: true });
}
