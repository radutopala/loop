import { claimTerminalSessions } from "../api/terminal";

/** How often a window claims its terminal sessions. The server closes a
 *  session no pane is attached to once no window claimed it for 5 minutes. */
export const CLAIM_INTERVAL_MS = 60_000;

let timer: ReturnType<typeof setInterval> | null = null;

/** Claims the sessions `held` returns now and then every minute, once per
 *  window however many panes call it. */
export function startTerminalClaims(held: () => string[]) {
  if (timer) return;
  const claim = () => {
    const ids = held();
    if (ids.length > 0) {
      claimTerminalSessions(ids).catch(() => {
        // The next claim retries; the server allows several missed ones.
      });
    }
  };
  claim();
  timer = setInterval(claim, CLAIM_INTERVAL_MS);
}
