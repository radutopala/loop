/** The part of an xterm buffer tailLines reads. */
export interface TextBuffer {
  readonly length: number;
  getLine(y: number): { translateToString(trimRight?: boolean): string } | undefined;
}

/** The buffer's last n lines of text, the blank lines below the last
 *  printed one left out. */
export function tailLines(buffer: TextBuffer, n: number): string {
  let end = buffer.length;
  while (end > 0 && !buffer.getLine(end - 1)?.translateToString(true)) end--;
  const lines: string[] = [];
  for (let y = Math.max(0, end - n); y < end; y++) lines.push(buffer.getLine(y)?.translateToString(true) ?? "");
  return lines.join("\n");
}
