import { AGENT_OPEN_MODE_OPTIONS, type AgentOpenMode, PANEL_OPTIONS, type PanelType } from "../types/panels";
import type { PaneInfo, PaneOptions, StepResult, TerminalInput, UiHost, UiStep, WorkspaceController } from "./types";

/** The panes send_input types into, and read_output and wait_for read:
 *  the agent's terminals, in its container. Never a host shell, which runs
 *  on the host. */
export const INPUT_PANELS: PanelType[] = ["docker-agent", "docker-shell"];

/** How long a command may take when the daemon gives no timeout. */
export const DEFAULT_TIMEOUT_MS = 60_000;

/** How long an agent terminal must be quiet before it's typed into: a
 *  starting Claude TUI prints for a while before it takes input. */
export const INPUT_QUIET_MS = 800;

/** How long wait_for wants a terminal quiet when the step doesn't say. */
export const WAIT_QUIET_MS = 2000;

/** How many lines read_output and wait_for return when the step doesn't
 *  say, and at most. */
export const DEFAULT_LINES = 50;
export const MAX_LINES = 2000;

const POLL_MS = 50;

const PASTE_START = "\x1b[200~";
const PASTE_END = "\x1b[201~";

/** What a step adds to its result. */
type StepOutput = Omit<StepResult, "op" | "ok">;

/** What the steps of one command share: the channel they run in, and when
 *  each pane was last typed into, for wait_for to wait for what that
 *  brought. */
interface Run {
  host: UiHost;
  deadline: number;
  /** The channel select_channel opened, or the one the first step found
   *  open. Once set, a step fails if the window shows another, so a
   *  command never follows the window into another channel. */
  channel?: string;
  sentAt: Map<string, number>;
}

class StepError extends Error {}

/**
 * Runs steps in order and returns their results, stopping at the first that
 * fails. timeoutMs is how long the daemon waits for them; steps that wait
 * (for a channel to open, a terminal to be ready) give up by then.
 */
export async function runSteps(steps: UiStep[], host: UiHost, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<StepResult[]> {
  const run: Run = { host, deadline: host.now() + timeoutMs, sentAt: new Map() };
  const results: StepResult[] = [];
  for (const step of steps) {
    try {
      results.push({ op: step.op, ok: true, ...(await runStep(step, run)) });
    } catch (err) {
      results.push({ op: step.op, ok: false, error: err instanceof Error ? err.message : String(err) });
      break;
    }
  }
  return results;
}

async function waitFor(run: Run, what: string, ready: () => boolean): Promise<void> {
  const { host, deadline } = run;
  while (!ready()) {
    if (host.now() >= deadline) throw new StepError(`timed out waiting for ${what}`);
    await host.sleep(POLL_MS);
    stillOn(run);
  }
}

/** Fails when the window no longer shows the command's channel, e.g. after
 *  the user opened another while a step waited. */
function stillOn(run: Run): void {
  const current = run.host.selectedChannelId();
  if (run.channel && current !== run.channel) throw new StepError(`the window left channel ${run.channel} for ${current ?? "no channel"}; a command's steps stay in its channel`);
}

function workspaceOf(run: Run): { channelId: string; ws: WorkspaceController } {
  stillOn(run);
  const channelId = run.host.selectedChannelId();
  const ws = channelId ? run.host.workspace(channelId) : undefined;
  if (!channelId || !ws) throw new StepError("no channel is open; select_channel first");
  run.channel ??= channelId;
  return { channelId, ws };
}

function splitWorkspace(run: Run): { channelId: string; ws: WorkspaceController } {
  const w = workspaceOf(run);
  if (w.ws.view().canvas) throw new StepError("the open tab is a canvas; pane steps work on split tabs");
  return w;
}

/** A pane by id, else the first pane of that panel type. */
function findPane(panes: PaneInfo[], pane: string | undefined): PaneInfo {
  if (!pane) throw new StepError("pane is required");
  const found = panes.find((p) => p.id === pane) ?? panes.find((p) => p.panel === pane);
  if (!found) throw new StepError(`no pane "${pane}" in the open tab; panes: ${panes.map((p) => p.id).join(", ") || "none"}`);
  return found;
}

function panelOf(panel: string | undefined): PanelType {
  if (!panel) throw new StepError("panel is required");
  if (!PANEL_OPTIONS.some((o) => o.panel === panel)) throw new StepError(`unknown panel "${panel}"`);
  return panel as PanelType;
}

function openModeOf(mode: string | undefined): AgentOpenMode | undefined {
  if (mode === undefined) return undefined;
  if (!AGENT_OPEN_MODE_OPTIONS.some((o) => o.mode === mode)) throw new StepError(`unknown open_mode "${mode}"; use resume, fork or fresh`);
  return mode as AgentOpenMode;
}

/** Runs a step against the controller, turning its Error into the step's. */
function attempt<T>(fn: () => T): T {
  try {
    return fn();
  } catch (err) {
    throw new StepError(err instanceof Error ? err.message : String(err));
  }
}

/** The playground a playground pane a step makes shows, checked against
 *  the channel's. */
async function paneOptions(step: UiStep, panel: PanelType, channelId: string, host: UiHost): Promise<PaneOptions> {
  const opts: PaneOptions = {};
  const openMode = openModeOf(step.open_mode);
  if (openMode) opts.openMode = openMode;
  if (step.item === undefined && step.scope === undefined) return opts;
  if (panel !== "playground") throw new StepError("item and scope are for playground panes");
  if (!step.item) throw new StepError("item is required with scope");
  if (step.scope !== undefined && step.scope !== "global" && step.scope !== "project") throw new StepError(`unknown scope "${step.scope}"; use global or project`);
  const all = await host.playgrounds(channelId);
  const found = all.filter((p) => p.name === step.item && (step.scope === undefined || p.scope === step.scope));
  if (found.length === 0) throw new StepError(`no playground "${step.item}"; playgrounds: ${all.map((p) => `${p.name} (${p.scope})`).join(", ") || "none"}`);
  if (found.length > 1) throw new StepError(`playground "${step.item}" is both global and project; give scope`);
  opts.playground = found[0];
  return opts;
}

function linesOf(step: UiStep): number {
  const lines = step.lines ?? DEFAULT_LINES;
  if (!Number.isInteger(lines) || lines < 1 || lines > MAX_LINES) throw new StepError(`lines must be a whole number from 1 to ${MAX_LINES}`);
  return lines;
}

async function runStep(step: UiStep, run: Run): Promise<StepOutput> {
  const { host } = run;
  switch (step.op) {
    case "select_channel": {
      const id = step.channel_id;
      if (!id) throw new StepError("channel_id is required");
      run.channel = undefined;
      if (host.selectedChannelId() !== id) await host.selectChannel(id);
      await waitFor(run, `channel ${id} to open`, () => host.selectedChannelId() === id && !!host.workspace(id));
      run.channel = id;
      return {};
    }
    case "set_tab": {
      const { ws } = workspaceOf(run);
      const tab = step.tab;
      if (!tab) throw new StepError("tab is required");
      const { tabs } = ws.view();
      if (!tabs.includes(tab)) throw new StepError(`no tab "${tab}"; tabs: ${tabs.join(", ")}`);
      if (ws.view().tab === tab) return {};
      ws.setTab(tab);
      // The tab's panes are there once it renders.
      await waitFor(run, `tab ${tab} to open`, () => ws.view().tab === tab);
      return {};
    }
    case "create_tab": {
      const { ws } = workspaceOf(run);
      const name = step.tab?.trim();
      if (step.tab !== undefined && !name) throw new StepError("tab can't be empty");
      if (name && ws.view().tabs.includes(name)) throw new StepError(`there's already a tab "${name}"`);
      const tab = ws.createTab(name);
      await waitFor(run, `tab ${tab} to open`, () => ws.view().tab === tab);
      return { tab };
    }
    case "rename_tab": {
      const { ws } = workspaceOf(run);
      const tab = step.tab;
      if (!tab) throw new StepError("tab is required");
      const name = step.name?.trim();
      if (!name) throw new StepError("name is required");
      const { tabs } = ws.view();
      if (!tabs.includes(tab)) throw new StepError(`no tab "${tab}"; tabs: ${tabs.join(", ")}`);
      if (name === tab) return { tab };
      if (tabs.includes(name)) throw new StepError(`there's already a tab "${name}"`);
      ws.renameTab(tab, name);
      await waitFor(run, `tab ${tab} to be renamed`, () => ws.view().tabs.includes(name));
      return { tab: name };
    }
    case "remove_tab": {
      const { ws } = workspaceOf(run);
      const tab = step.tab;
      if (!tab) throw new StepError("tab is required");
      const { tabs } = ws.view();
      if (!tabs.includes(tab)) throw new StepError(`no tab "${tab}"; tabs: ${tabs.join(", ")}`);
      if (tabs.length <= 1) throw new StepError("the last tab can't be removed");
      ws.removeTab(tab);
      await waitFor(run, `tab ${tab} to close`, () => !ws.view().tabs.includes(tab));
      return {};
    }
    case "replace_pane": {
      const { channelId, ws } = splitWorkspace(run);
      const pane = findPane(ws.view().panes, step.pane);
      const panel = panelOf(step.panel);
      const opts = await paneOptions(step, panel, channelId, host);
      stillOn(run);
      return { pane: attempt(() => ws.replacePane(pane.id, panel, opts)) };
    }
    case "add_pane": {
      const { channelId, ws } = splitWorkspace(run);
      const panel = panelOf(step.panel);
      const nextTo = step.next_to === undefined ? undefined : findPane(ws.view().panes, step.next_to).id;
      const direction = step.direction ?? "horizontal";
      if (direction !== "horizontal" && direction !== "vertical") throw new StepError(`unknown direction "${direction}"; use horizontal or vertical`);
      const side = step.side ?? "after";
      if (side !== "before" && side !== "after") throw new StepError(`unknown side "${side}"; use before or after`);
      const opts = await paneOptions(step, panel, channelId, host);
      stillOn(run);
      return { pane: attempt(() => ws.addPane(panel, nextTo, direction, side === "before", opts)) };
    }
    case "remove_pane": {
      const { ws } = splitWorkspace(run);
      const pane = findPane(ws.view().panes, step.pane);
      attempt(() => ws.removePane(pane.id));
      return {};
    }
    case "maximize_pane": {
      const { ws } = splitWorkspace(run);
      const pane = findPane(ws.view().panes, step.pane);
      ws.maximize(pane.id);
      return { pane: pane.id };
    }
    case "restore_pane": {
      const { ws } = splitWorkspace(run);
      ws.maximize(null);
      return {};
    }
    case "open_file": {
      const { channelId } = splitWorkspace(run);
      if (!step.path) throw new StepError("path is required");
      if (step.line !== undefined && (!Number.isInteger(step.line) || step.line < 1)) throw new StepError("line must be a whole number from 1");
      await host.openFile(channelId, step.path, step.line).catch((err) => {
        throw new StepError(err instanceof Error ? err.message : String(err));
      });
      return {};
    }
    case "send_input":
      return sendInput(step, run);
    case "read_output": {
      const lines = linesOf(step);
      const { pane, t } = await terminalOf(step, run, "reads");
      return { pane, output: t.read(lines) };
    }
    case "wait_for":
      return waitForOutput(step, run);
    default:
      throw new StepError(`unknown op "${step.op}"`);
  }
}

/** The agent terminal of the step's pane, once it's there and its session
 *  has started, or ready (quiet) when ready is set. */
async function terminalOf(step: UiStep, run: Run, verb: string, ready?: (t: TerminalInput) => boolean): Promise<{ channelId: string; pane: string; t: TerminalInput }> {
  const { host } = run;
  const { channelId, ws } = workspaceOf(run);
  const pane = findPane(ws.view().panes, step.pane);
  if (!INPUT_PANELS.includes(pane.panel)) throw new StepError(`${step.op} only ${verb} docker-agent and docker-shell panes, not ${pane.panel}`);
  // A pane added a step before mounts and starts its session first.
  let found: TerminalInput | undefined;
  await waitFor(run, `pane ${pane.id} to be ${ready ? "ready for input" : "started"}`, () => {
    found = host.terminal(channelId, pane.id);
    if (!found) return false;
    const status = found.status();
    if (ready && (status === "completed" || status === "failed")) throw new StepError(`pane ${pane.id}'s session has ${status === "failed" ? "failed" : "ended"}`);
    return ready ? ready(found) : status !== "connecting";
  });
  return { channelId, pane: pane.id, t: found as TerminalInput };
}

async function sendInput(step: UiStep, run: Run): Promise<StepOutput> {
  if (typeof step.text !== "string") throw new StepError("text is required");
  const { host } = run;
  const { pane, t } = await terminalOf(step, run, "types into", (t) => t.status() === "running" && t.lastOutputAt() > 0 && host.now() - t.lastOutputAt() >= INPUT_QUIET_MS);
  const submit = step.submit ?? true;
  if (t.kind === "agent") {
    // The Claude TUI: a bracketed paste keeps a multi-line prompt one
    // input, and \r submits it. A marker in the text would end the paste
    // early and send the rest as keys.
    if (step.text.includes(PASTE_START) || step.text.includes(PASTE_END)) throw new StepError("text can't contain a bracketed-paste marker (ESC [200~ or ESC [201~)");
    t.send(`${PASTE_START}${step.text}${PASTE_END}${submit ? "\r" : ""}`);
  } else {
    t.send(`${step.text}${submit ? "\n" : ""}`);
  }
  run.sentAt.set(pane, host.now());
  return { pane };
}

/** Waits until the pane's output matches step.match, or, without one, until
 *  it's been quiet for step.quiet_ms; after a send_input to the pane earlier
 *  in the command, only output since counts. A session that ends is done. */
async function waitForOutput(step: UiStep, run: Run): Promise<StepOutput> {
  const lines = linesOf(step);
  let match: RegExp | undefined;
  if (step.match !== undefined) {
    try {
      match = new RegExp(step.match, "m");
    } catch (err) {
      throw new StepError(`match: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  const quietMs = step.quiet_ms ?? WAIT_QUIET_MS;
  if (!Number.isInteger(quietMs) || quietMs < 0) throw new StepError("quiet_ms must be a whole number from 0");
  const { host } = run;
  const found = await terminalOf(step, run, "reads");
  const { channelId, pane } = found;
  let t = found.t;
  const since = run.sentAt.get(pane) ?? 0;
  await waitFor(run, match ? `pane ${pane} to print /${step.match}/` : `pane ${pane} to be quiet`, () => {
    // A pane that remounted has a new terminal.
    const current = host.terminal(channelId, pane);
    if (!current) throw new StepError(`pane ${pane} is no longer open`);
    t = current;
    const status = t.status();
    if (status === "completed" || status === "failed") return true;
    const last = t.lastOutputAt();
    if (last <= since) return false;
    if (match) return match.test(t.read(lines));
    return host.now() - last >= quietMs;
  });
  return { pane, output: t.read(lines) };
}
