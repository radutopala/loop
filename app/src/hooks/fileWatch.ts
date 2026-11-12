import { makePathKey, parsePathKey } from "../components/panels/EditorFileTree";

/** What /files/watch reports: a watched file changed on disk, or a watched
 * directory's entries did. */
export interface FileWatchChange {
  type: "file" | "dir";
  pathKey: string;
}

function ref(pathKey: string) {
  const { rootIndex, relativePath } = parsePathKey(pathKey);
  return { root: rootIndex, path: relativePath };
}

/** The "watch" message for the open tabs and the expanded tree dirs. */
export function fileWatchMessage(openTabs: string[], expandedDirs: Iterable<string>): string {
  return JSON.stringify({ type: "watch", files: openTabs.map(ref), dirs: [...expandedDirs].map(ref) });
}

/** Parses one /files/watch message; anything else (errors included) is null. */
export function parseFileWatchChange(data: unknown): FileWatchChange | null {
  if (typeof data !== "string") return null;
  try {
    const msg = JSON.parse(data) as { type?: unknown; root?: unknown; path?: unknown };
    if (msg.type !== "file" && msg.type !== "dir") return null;
    const root = typeof msg.root === "number" ? msg.root : 0;
    const path = typeof msg.path === "string" ? msg.path : "";
    return { type: msg.type, pathKey: makePathKey(root, path) };
  } catch {
    return null;
  }
}
