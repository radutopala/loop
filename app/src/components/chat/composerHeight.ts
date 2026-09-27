// Height of the chat composer's textarea for its current content: never
// shorter than minRows lines, never taller than maxPx (past that it scrolls).
// scrollHeight is measured with the height reset to "auto", so it is the
// content's natural height including the textarea's vertical padding.
export function composerHeight(scrollHeight: number, lineHeightPx: number, verticalPaddingPx: number, minRows: number, maxPx: number): { height: number; scrolls: boolean } {
  const min = minRows * lineHeightPx + verticalPaddingPx;
  const natural = Math.max(scrollHeight, min);
  if (natural > maxPx) return { height: Math.max(maxPx, min), scrolls: true };
  return { height: natural, scrolls: false };
}

// The tallest the composer grows before it scrolls: 40% of the window, capped.
export function composerMaxHeight(windowHeight: number): number {
  return Math.min(Math.round(windowHeight * 0.4), 360);
}
