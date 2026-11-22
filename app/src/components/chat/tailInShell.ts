import type { BackgroundTask } from "../../types";
import { getTerminalInput, getWorkspace } from "../../uiBridge/registry";
import { SIDE_PANE } from "../../uiBridge/sideText";
import { redirects } from "./shellRedirects";

/** How long to wait for the Shell view to open and its shell to start. */
export const SHELL_READY_TIMEOUT_MS = 15_000;
const POLL_MS = 100;
/** The pause after Ctrl+C: the terminal drops input that comes with the
 *  interrupt, so the command must come after it. */
export const INTERRUPT_PAUSE_MS = 150;

/** Quotes s as one shell word. */
export function shellQuote(s: string): string {
  return `'${s.replaceAll("'", `'\\''`)}'`;
}

/** A file to follow, as the shell word tail takes (word) and as it's shown
 *  (shown). */
export interface TailFile {
  word: string;
  shown: string;
}

/** A task's output file, the CLI's own; undefined when it isn't known. The
 *  CLI names the file of a background command, but never a Monitor's. That
 *  one lies in the same folder as the session's other task files, which the
 *  shell finds: the folder's root holds the agent's uid and its parent
 *  names the project, so both are matched, and only the ids are quoted. */
export function outputFile(t: BackgroundTask): TailFile | undefined {
  if (t.output_path) return { word: shellQuote(t.output_path), shown: t.output_path };
  if (t.type !== "monitor" || !t.session_id || !t.id) return undefined;
  const tail = `/tasks/${t.id}.output`;
  return { word: `/tmp/claude-*/*/${shellQuote(t.session_id)}${shellQuote(tail)}`, shown: `/tmp/claude-*/*/${t.session_id}${tail}` };
}

/** path as a shell word: quoted, with a leading ~ left for the shell. */
function pathWord(path: string): string {
  if (path === "~") return "~";
  return path.startsWith("~/") ? `~/${shellQuote(path.slice(2))}` : shellQuote(path);
}

/** What Tail follows for a task. */
export interface TailPlan {
  files: TailFile[];
  /** The task waited on, when the files are its: one whose output file the
   *  task's command reads. */
  waitsOn?: BackgroundTask;
  /** The filter that holds the output back; see Redirects. */
  heldBy?: string;
}

/**
 * What Tail follows for t, one of tasks: the files its command redirects
 * its output to, then its own output file, which gets the rest (an exit
 * code it echoes, most often). A command that only waits on another of
 * tasks, reading that one's output file, follows that one's files. A
 * relative file is followed from the Shell view's directory. Undefined when
 * there's nothing to follow.
 */
export function tailPlan(t: BackgroundTask, tasks: BackgroundTask[] = []): TailPlan | undefined {
  const own = outputFile(t);
  const r = t.command ? redirects(t.command) : { files: [] };
  if (!r.files.length && t.command) {
    for (const other of tasks) {
      if (other === t || !other.id || !other.command || !t.command.includes(`/tasks/${other.id}.output`)) continue;
      const plan = redirects(other.command).files.length ? tailPlan(other) : undefined;
      if (plan) return { ...plan, waitsOn: other };
    }
  }
  const files = r.files.map((f) => ({ word: pathWord(f), shown: f }));
  if (own) files.push(own);
  if (!files.length) return undefined;
  return r.heldBy ? { files, heldBy: r.heldBy } : { files };
}

/** Prints lines dimmed, as comments. */
function notes(lines: string[]): string {
  return `printf '\\033[2m# %s\\033[0m\\n' ${lines.map(shellQuote).join(" ")}`;
}

/**
 * The shell line that follows plan's files for task t: it clears the view,
 * says what it follows, and why nothing may show yet, then tails the files
 * from their start, through their being replaced as a command rerun.
 */
export function tailCommand(t: BackgroundTask, plan: TailPlan): string {
  const words = plan.files.map((f) => f.word).join(" ");
  const about: string[] = [];
  if (t.description) about.push(t.description);
  if (plan.waitsOn) about.push(`waits on: ${plan.waitsOn.description || plan.waitsOn.id}`);
  about.push(`following ${plan.files.map((f) => f.shown).join(", ")}`);
  if (plan.heldBy) about.push(`its output goes through ${plan.heldBy}, which may hold it back until it ends`);
  const empty = notes(["no output yet: a command that writes nothing until it ends shows it then"]);
  return `clear; ${notes(about)}; [ -n "$(cat ${words} 2>/dev/null | head -c 1)" ] || ${empty}; tail -n +1 -F ${words}`;
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/**
 * Opens the chat's Shell view and runs line there, after Ctrl+C ends
 * whatever the shell ran, an earlier tail most often. The Shell view opens
 * only once the chat's container is known to run, which can take a poll
 * after the chat shows, so it's tried until then. Rejects when channelId
 * has no open workspace, or the Shell view or its shell doesn't start in
 * time.
 */
export async function tailInShell(channelId: string, line: string, sleep: (ms: number) => Promise<void> = pause): Promise<void> {
  if (!getWorkspace(channelId)) throw new Error(`channel ${channelId} isn't open`);
  for (let waited = 0; ; waited += POLL_MS) {
    const ws = getWorkspace(channelId);
    let opened = false;
    try {
      if (ws && ws.view().side !== "shell") ws.openSide("shell");
      opened = !!ws;
    } catch (err) {
      if (waited >= SHELL_READY_TIMEOUT_MS) throw err;
    }
    // Started and has printed its prompt; a tail that still prints never
    // goes quiet, so being quiet isn't asked for.
    const t = opened ? getTerminalInput(channelId, SIDE_PANE) : undefined;
    const status = t?.status();
    if (t && status === "running" && t.lastOutputAt() > 0) {
      t.send("\x03");
      await sleep(INTERRUPT_PAUSE_MS);
      t.send(`${line}\n`);
      return;
    }
    if (status === "completed" || status === "failed") throw new Error("the Shell view's shell has ended");
    if (waited >= SHELL_READY_TIMEOUT_MS) throw new Error(opened ? "the Shell view's shell didn't start" : `channel ${channelId} isn't open`);
    await sleep(POLL_MS);
  }
}
