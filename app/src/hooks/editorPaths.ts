import type { RootEntry } from "../api/files";
import { makePathKey } from "../components/panels/EditorFileTree";

/**
 * Map a tool's absolute file path to a `{rootIndex}:{relativePath}` pathKey,
 * picking the LONGEST matching root prefix so nested roots resolve to the most
 * specific one (e.g. `/repo/sub` wins over `/repo` for `/repo/sub/file.ts`).
 * A path equal to a root maps to that root with an empty relative path.
 * Returns null when no root contains the path. Pure — extracted from
 * useEditorState so it can be unit-tested without the hook's dependencies.
 */
export function matchAbsPathToKey(absPath: string, roots: RootEntry[]): string | null {
  let best: { root: RootEntry; rel: string } | null = null;
  for (const root of roots) {
    const base = root.path.endsWith("/") ? root.path.slice(0, -1) : root.path;
    if (absPath === base) {
      if (!best || base.length > best.root.path.length) best = { root, rel: "" };
      continue;
    }
    const prefix = base + "/";
    if (absPath.startsWith(prefix)) {
      const rel = absPath.slice(prefix.length);
      if (!best || base.length > (best.root.path.endsWith("/") ? best.root.path.length - 1 : best.root.path.length)) {
        best = { root, rel };
      }
    }
  }
  if (!best) return null;
  return makePathKey(best.root.index, best.rel);
}
