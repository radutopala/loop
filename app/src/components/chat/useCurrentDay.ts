import { useSyncExternalStore } from "react";

// One timer, shared by every subscriber, fires at the next local midnight so
// memoized components that print day-relative dates re-render on rollover.
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | undefined;

function msUntilNextDay(now: Date): number {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime() - now.getTime();
}

function schedule() {
  timer = setTimeout(() => {
    schedule();
    for (const listener of listeners) listener();
  }, msUntilNextDay(new Date()));
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) schedule();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      clearTimeout(timer);
      timer = undefined;
    }
  };
}

const today = () => new Date().toDateString();

/** The current local day, as a string that changes at midnight. */
export function useCurrentDay(): string {
  return useSyncExternalStore(subscribe, today);
}
