import { useEffect } from "react";

/** Copy text to clipboard, with non-secure context (HTTP) fallback. */
export function copyText(text: string): void {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => {});
    return;
  }
  // Non-secure context fallback: create a temporary textarea for DOM-level selection,
  // intercept the copy event to set clipboardData explicitly.
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.cssText = "position:fixed;left:-9999px;top:-9999px";
  document.body.appendChild(textarea);
  textarea.select();
  const onCopy = (e: ClipboardEvent) => {
    e.clipboardData?.setData("text/plain", text);
    e.preventDefault();
  };
  document.addEventListener("copy", onCopy);
  document.execCommand("copy");
  document.removeEventListener("copy", onCopy);
  document.body.removeChild(textarea);
}

/**
 * Top-level hook: auto-copies any text selection on mouseup.
 * Handles both DOM selections (chat, editor, notes, git, etc.) and
 * xterm selections (agent terminal, shell) via a getter attached to the
 * .xterm DOM element by useXTerminal.
 * Skips text inputs/textareas (e.g. chat input) so normal typing isn't affected.
 * Call once at the workspace root.
 */
export function useCopyOnSelect(): void {
  useEffect(() => {
    const onMouseUp = (e: MouseEvent) => {
      // 1. Check DOM selection (covers all non-terminal panels).
      const sel = window.getSelection();
      const text = sel?.toString();
      if (text) {
        const anchor = sel?.anchorNode;
        const el = anchor instanceof HTMLElement ? anchor : anchor?.parentElement;
        if (!el?.closest("textarea, input")) {
          copyText(text);
          return;
        }
      }
      // 2. Check xterm selection via getter attached to the .xterm element.
      const target = e.target as HTMLElement;
      const xtermEl = target.closest?.(".xterm") as (Element & { _xtermGetSelection?: () => string }) | null;
      if (xtermEl?._xtermGetSelection) {
        const xtermText = xtermEl._xtermGetSelection();
        if (xtermText) {
          copyText(xtermText);
        }
      }
    };
    document.addEventListener("mouseup", onMouseUp);
    return () => document.removeEventListener("mouseup", onMouseUp);
  }, []);
}
