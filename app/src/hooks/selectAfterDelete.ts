/**
 * selectAfterDelete is the channel to select once deletedIds are gone:
 * undefined keeps the selection (it wasn't deleted), the nearest ancestor
 * that stays when the selected thread or worktree thread was deleted, and
 * null (the empty view) when a channel was.
 */
export function selectAfterDelete(selectedId: string | null, deletedIds: readonly string[], channels: readonly { id: string; parent_id?: string }[]): string | null | undefined {
  if (!selectedId || !deletedIds.includes(selectedId)) return undefined;
  const parentOf = new Map(channels.map((c) => [c.id, c.parent_id]));
  const seen = new Set<string>();
  let id: string | undefined = parentOf.get(selectedId);
  while (id && !seen.has(id)) {
    if (!deletedIds.includes(id)) return parentOf.has(id) ? id : null;
    seen.add(id);
    id = parentOf.get(id);
  }
  return null;
}
