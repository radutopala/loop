// The sidebar's "needs you" filter keeps only the rows with a lit pill
// (an approval, a question, a plan, a review, a config to trust), plus the
// ancestors that lead to them so the tree still reads as a tree.

/**
 * Prunes a parent → threads map down to the threads that are lit or have a
 * lit thread somewhere below them. Parents left with no threads are dropped.
 */
export function pruneToLit<T extends { id: string }>(threadsByParent: Record<string, ReadonlyArray<T>>, lit: (id: string) => boolean): Record<string, T[]> {
  const kept = new Map<string, boolean>();
  const keep = (id: string, seen: Set<string>): boolean => {
    const known = kept.get(id);
    if (known !== undefined) return known;
    // A cycle in parent ids would otherwise recurse forever.
    if (seen.has(id)) return false;
    seen.add(id);
    const children = threadsByParent[id] ?? [];
    // Every child is visited, not just up to the first kept one, so each
    // child's own verdict is recorded for the pruned lists below.
    const childKept = children.map((c) => keep(c.id, seen)).some(Boolean);
    const result = lit(id) || childKept;
    kept.set(id, result);
    return result;
  };
  const out: Record<string, T[]> = {};
  for (const [parentId, threads] of Object.entries(threadsByParent)) {
    const list = threads.filter((t) => keep(t.id, new Set([parentId])));
    if (list.length > 0) out[parentId] = list;
  }
  return out;
}
