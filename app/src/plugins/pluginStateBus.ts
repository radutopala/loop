// Tells the other frames showing a plugin panel's state (the same panel of
// the same channel in another pane) when one of them saves it.
type Listener = (value: unknown) => void;

const subs = new Map<string, Set<Listener>>();

/** Calls listener with the state each time another frame saves key's; returns
 *  the unsubscribe function. */
export function onPluginState(key: string, listener: Listener): () => void {
  let set = subs.get(key);
  if (!set) subs.set(key, (set = new Set()));
  set.add(listener);
  return () => {
    set.delete(listener);
    if (set.size === 0) subs.delete(key);
  };
}

/** Tells key's listeners but from of the state just saved. */
export function emitPluginState(key: string, value: unknown, from: Listener): void {
  for (const l of subs.get(key) ?? []) if (l !== from) l(value);
}
