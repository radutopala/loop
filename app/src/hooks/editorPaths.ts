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

/**
 * The absolute file path an agent Edit/Write tool call writes, or null for
 * other tools. The tool.use event carries a summary of the call's input, not
 * the input itself, and for these tools the summary is the `file_path`.
 */
export function editToolFilePath(toolName: string, input: string): string | null {
  if (toolName !== "Edit" && toolName !== "Write") return null;
  return input.startsWith("/") ? input : null;
}

/** An open editor tab's location, as shown in its label. */
export interface TabPathInfo {
  key: string;
  rootName: string;
  relativePath: string;
}

/**
 * The dim text shown next to each tab's file name, keyed by tab key: the
 * folder the file lives in, led by its root's name when several roots are
 * open (".loop › container"). A file's parent folder alone is shown unless
 * another open tab has the same name in the same root; then enough parent
 * folders are added to tell them apart, with "…/" marking the omitted rest.
 */
export function tabDescriptions(tabs: TabPathInfo[], showRoot: boolean): Map<string, string> {
  const dirsOf = (t: TabPathInfo) => t.relativePath.split("/").slice(0, -1);
  const groups = new Map<string, TabPathInfo[]>();
  for (const t of tabs) {
    const name = t.relativePath.split("/").pop() ?? "";
    const group = (showRoot ? t.rootName : "") + "\0" + name;
    groups.set(group, [...(groups.get(group) ?? []), t]);
  }
  const out = new Map<string, string>();
  for (const group of groups.values()) {
    const maxDepth = Math.max(1, ...group.map((t) => dirsOf(t).length));
    let depth = 1;
    while (depth < maxDepth && new Set(group.map((t) => dirsOf(t).slice(-depth).join("/"))).size < group.length) depth++;
    for (const t of group) {
      const dirs = dirsOf(t);
      const shown = dirs.slice(-depth).join("/");
      const dir = depth < dirs.length ? `…/${shown}` : shown;
      out.set(t.key, [showRoot ? t.rootName : "", dir].filter(Boolean).join(" › "));
    }
  }
  return out;
}
