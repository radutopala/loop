// Finds a walkthrough step's lines in a file that may have changed since the
// walkthrough was made: the step keeps the text its lines had then (its
// anchor), so they can be found where they are now.

/** Where a step's lines are in the file now: "same" where they were, "moved"
 *  found elsewhere, "changed" not found, so shown where they were. */
export interface StepRange {
  /** 1-based, inclusive. */
  start: number;
  end: number;
  status: "same" | "moved" | "changed";
}

/** How many of a long anchor's first lines are matched when the whole of it
 *  isn't found: it may have been cut short, or its end edited. */
const PREFIX_LINES = 5;

/** The starts of the lines of text, 0-based by line. */
function lineStarts(text: string): number[] {
  const starts = [0];
  for (let i = text.indexOf("\n"); i !== -1; i = text.indexOf("\n", i + 1)) starts.push(i + 1);
  return starts;
}

/** The 1-based line of offset. */
function lineAt(starts: number[], offset: number): number {
  let lo = 0;
  let hi = starts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if ((starts[mid] as number) <= offset) lo = mid;
    else hi = mid - 1;
  }
  return lo + 1;
}

/** The 1-based lines needle starts on in text, as a whole line's start. */
function occurrences(text: string, needle: string, starts: number[]): number[] {
  const lines: number[] = [];
  for (let i = text.indexOf(needle); i !== -1; i = text.indexOf(needle, i + 1)) {
    if (i === 0 || text[i - 1] === "\n") lines.push(lineAt(starts, i));
  }
  return lines;
}

/** The line of lines nearest to line. */
const nearest = (lines: number[], line: number) => lines.reduce((best, l) => (Math.abs(l - line) < Math.abs(best - line) ? l : best));

/**
 * locateStep finds the step's lines in text: where they were when its anchor
 * still starts there, else the occurrence of the anchor (or, for one of
 * several lines, of its first lines) nearest to where they were. Not found,
 * they're where they were, kept within the file.
 */
export function locateStep(text: string, startLine: number, endLine: number, anchor: string | undefined): StepRange {
  const starts = lineStarts(text);
  const total = starts.length;
  const clamp = (n: number) => Math.max(1, Math.min(n, total));
  const span = Math.max(0, endLine - startLine);
  const at = (start: number, status: StepRange["status"]): StepRange => ({ start: clamp(start), end: clamp(start + span), status });
  if (!anchor) return at(startLine, "same");
  const original = clamp(startLine);
  if (original === startLine && text.startsWith(anchor, starts[original - 1])) return at(startLine, "same");
  let found = occurrences(text, anchor, starts);
  if (found.length === 0) {
    // The last line of a long anchor may be cut short: leave it out.
    const lines = anchor.split("\n");
    const prefix = lines.slice(0, Math.min(PREFIX_LINES, lines.length - 1)).join("\n");
    if (prefix.trim()) found = occurrences(text, prefix, starts);
  }
  if (found.length === 0) return at(startLine, "changed");
  const line = nearest(found, startLine);
  return at(line, line === startLine ? "same" : "moved");
}
