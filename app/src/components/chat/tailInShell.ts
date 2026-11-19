import type { BackgroundTask } from "../../types";
import { getTerminalInput, getWorkspace } from "../../uiBridge/registry";
import { SIDE_PANE } from "../../uiBridge/sideText";

/** How long to wait for the Shell view's shell to start. */
export const SHELL_READY_TIMEOUT_MS = 15_000;
const POLL_MS = 100;
/** The pause after Ctrl+C: the terminal drops input that comes with the
 *  interrupt, so the command must come after it. */
export const INTERRUPT_PAUSE_MS = 150;

/** Quotes s as one shell word. */
export function shellQuote(s: string): string {
  return `'${s.replaceAll("'", `'\\''`)}'`;
}

/** A task's output file, as the shell word tail takes (word) and as its
 *  tip shows it (shown); undefined when it isn't known. The CLI names the
 *  file of a background command, but never a Monitor's. That one lies in
 *  the same folder as the session's other task files, which the shell
 *  finds: the folder's root holds the agent's uid and its parent names
 *  the project, so both are matched, and only the ids are quoted. */
export function outputFile(t: BackgroundTask): { word: string; shown: string } | undefined {
  if (t.output_path) return { word: shellQuote(t.output_path), shown: t.output_path };
  if (t.type !== "monitor" || !t.session_id || !t.id) return undefined;
  const tail = `/tasks/${t.id}.output`;
  return { word: `/tmp/claude-*/*/${shellQuote(t.session_id)}${shellQuote(tail)}`, shown: `/tmp/claude-*/*/${t.session_id}${tail}` };
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/**
 * Opens the chat's Shell view and follows file, a shell word (see
 * outputFile), there with tail: Ctrl+C
 * first ends whatever the shell ran, an earlier tail most often. Rejects
 * when channelId has no open workspace, or its shell doesn't start in time.
 */
export async function tailInShell(channelId: string, file: string, sleep: (ms: number) => Promise<void> = pause): Promise<void> {
  const ws = getWorkspace(channelId);
  if (!ws) throw new Error(`channel ${channelId} isn't open`);
  if (ws.view().side !== "shell") ws.openSide("shell");
  // Started and has printed its prompt; a tail that still prints never
  // goes quiet, so being quiet isn't asked for.
  for (let waited = 0; ; waited += POLL_MS) {
    const t = getTerminalInput(channelId, SIDE_PANE);
    const status = t?.status();
    if (t && status === "running" && t.lastOutputAt() > 0) {
      t.send("\x03");
      await sleep(INTERRUPT_PAUSE_MS);
      t.send(`tail -n +1 -f ${file}\n`);
      return;
    }
    if (status === "completed" || status === "failed") throw new Error("the Shell view's shell has ended");
    if (waited >= SHELL_READY_TIMEOUT_MS) throw new Error("the Shell view's shell didn't start");
    await sleep(POLL_MS);
  }
}
