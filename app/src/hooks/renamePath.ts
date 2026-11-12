/** Where pathKey ends up when `from` (a file or a directory) is renamed to
 * `to`: `to` itself, the same place under `to`, or unchanged. */
export function renamedPathKey(pathKey: string, from: string, to: string): string {
  if (pathKey === from) return to;
  if (pathKey.startsWith(from + "/")) return to + pathKey.slice(from.length);
  return pathKey;
}

/** A copy of set with its keys renamed; set itself when none moves. */
export function renameSetKeys(set: Set<string>, from: string, to: string): Set<string> {
  const keys = [...set];
  const moved = keys.map((k) => renamedPathKey(k, from, to));
  return moved.some((k, i) => k !== keys[i]) ? new Set(moved) : set;
}

/** Renames map's keys in place. */
export function renameMapKeys<V>(map: Map<string, V>, from: string, to: string): Map<string, V> {
  for (const [k, v] of [...map]) {
    const moved = renamedPathKey(k, from, to);
    if (moved !== k) {
      map.delete(k);
      map.set(moved, v);
    }
  }
  return map;
}
