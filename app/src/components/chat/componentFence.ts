// Fenced blocks in chat markdown. A fence opens on a line starting with three
// or more backticks and closes on a line of only backticks at least as long,
// so a longer fence can hold shorter ones — which is how a chat component's
// document (it may itself contain ``` lines) travels inside its fence.

export type Fence = {
  info: string;
  body: string;
  closed: boolean;
  next: number;
};

// componentTag is the info-string tag of a chat component block, posted by the
// chat_component MCP tool: ```loop-component <template> <title>
export const componentTag = "loop-component";

// readFence reads the fenced block opening at lines[start], or returns null
// when that line doesn't open one. An unclosed fence runs to the end.
export function readFence(lines: string[], start: number): Fence | null {
  const open = /^(`{3,})(.*)$/.exec(lines[start] ?? "");
  if (!open) return null;
  const ticks = open[1]?.length ?? 0;
  const body: string[] = [];
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const close = /^(`{3,})\s*$/.exec(line);
    if (close && (close[1]?.length ?? 0) >= ticks) {
      return { info: (open[2] ?? "").trim(), body: body.join("\n"), closed: true, next: i + 1 };
    }
    body.push(line);
  }
  return { info: (open[2] ?? "").trim(), body: body.join("\n"), closed: false, next: lines.length };
}

// parseComponentInfo reads a component fence's info string, or returns null
// when the fence isn't a component.
export function parseComponentInfo(info: string): { template: string; title: string } | null {
  const m = new RegExp(`^${componentTag}\\s+([a-z0-9][a-z0-9_-]*)(?:\\s+(.*))?$`).exec(info);
  if (!m) return null;
  return { template: m[1] ?? "", title: (m[2] ?? "").trim() };
}

// componentHeightMessage is what a component's document posts to report its
// height; the frame is sized to it within these bounds.
export const componentHeightMessage = "loop-component-height";
export const componentMinHeight = 80;
export const componentMaxHeight = 900;

export function componentHeight(data: unknown): number | null {
  if (typeof data !== "object" || data === null) return null;
  const { type, height } = data as { type?: unknown; height?: unknown };
  if (type !== componentHeightMessage || typeof height !== "number" || !Number.isFinite(height)) return null;
  return Math.min(componentMaxHeight, Math.max(componentMinHeight, Math.ceil(height)));
}
