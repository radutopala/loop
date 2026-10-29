import { RE2JS } from "re2js";
import { SIDE_VIEW_LABEL, type SideView } from "../components/layout/LearnSplit";
import type { PluginPaneOption } from "../plugins/PluginPanelsContext";
import { AGENT_OPEN_MODE_OPTIONS, type AgentOpenMode, openModesFor, PANEL_OPTIONS, type PanelType } from "../types/panels";
import { SIDE_PANE } from "./sideText";
import type { ChatLine, ChatPaneInput, PaneInfo, PaneOptions, SideReader, StepResult, TerminalInput, UiHost, UiStep, WorkspaceController } from "./types";

/** The terminal panes send_input types into, and read_output and wait_for
 *  read: the agent's terminals, in its container. Never a host shell, which
 *  runs on the host. */
export const INPUT_PANELS: PanelType[] = ["docker-agent", "docker-shell"];

/** The chat panes send_input queues a message in, and read_output and
 *  wait_for read the messages of: the channel's chat and its side chats. */
export const CHAT_PANELS: PanelType[] = ["chat", "chat-side"];

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

/** The slowest pace open_walkthrough plays at, in seconds a step. */
const MAX_AUTOPLAY_SEC = 600;

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
  /** The last message of each chat pane when a message was queued in it,
   *  for wait_for to wait for the reply that brought. */
  chatSince: Map<string, number>;
}

class StepError extends Error {}

/**
 * Runs steps in order and returns their results, stopping at the first that
 * fails. timeoutMs is how long the daemon waits for them; steps that wait
 * (for a channel to open, a terminal to be ready) give up by then.
 */
export async function runSteps(steps: UiStep[], host: UiHost, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<StepResult[]> {
  const run: Run = { host, deadline: host.now() + timeoutMs, sentAt: new Map(), chatSince: new Map() };
  const results: StepResult[] = [];
  for (const step of steps) {
    try {
      results.push({ op: step.op, ok: true, ...(await runStep(withPaneRef(step, results), run)) });
    } catch (err) {
      results.push({ op: step.op, ok: false, error: err instanceof Error ? err.message : String(err) });
      break;
    }
  }
  return results;
}

/** PANE_REF is a pane as "$n": the pane step n (from 1) of the command made. */
const PANE_REF = /^\$(\d+)$/;

/** withPaneRef returns step with a "$n" pane, next_to or pane it names
 *  replaced by the pane step n made, which the steps run so far reported. */
function withPaneRef(step: UiStep, results: StepResult[]): UiStep {
  const resolve = (pane: string | undefined): string | undefined => {
    const ref = pane === undefined ? null : PANE_REF.exec(pane);
    if (!ref) return pane;
    const made = results[Number(ref[1]) - 1]?.pane;
    if (!made) throw new StepError(`${pane} names step ${ref[1]}'s pane, but no step before this one by that number made a pane`);
    return made;
  };
  return { ...step, pane: resolve(step.pane), next_to: resolve(step.next_to) };
}

async function waitFor(run: Run, what: string, ready: () => boolean): Promise<void> {
  await waitForValue(run, what, () => (ready() ? true : null));
}

/** Waits until get returns a value, and returns it. */
async function waitForValue<T>(run: Run, what: string, get: () => T | null): Promise<T> {
  const { host, deadline } = run;
  for (let value = get(); ; value = get()) {
    if (value !== null) return value;
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

/** A pane by id, else the first pane of that panel type. */
function findPane(panes: PaneInfo[], pane: string | undefined): PaneInfo {
  if (!pane) throw new StepError("pane is required");
  const found = panes.find((p) => p.id === pane) ?? panes.find((p) => p.panel === pane);
  if (!found) throw new StepError(`no pane "${pane}" in the open tab; panes: ${panes.map((p) => p.id).join(", ") || "none"}`);
  return found;
}

/** The side views open_side opens. */
const SIDE_VIEWS = Object.keys(SIDE_VIEW_LABEL) as SideView[];

function sideViewOf(view: string | undefined): SideView {
  if (!view) throw new StepError(`view is required: ${SIDE_VIEWS.join(", ")}`);
  if (!SIDE_VIEWS.includes(view as SideView)) throw new StepError(`unknown view "${view}"; use ${SIDE_VIEWS.join(", ")}`);
  return view as SideView;
}

const onSide = (step: UiStep) => step.pane === SIDE_PANE;

/** The side view open beside the chat, which the side pane is. */
function sideOf(run: Run): { channelId: string; view: SideView } {
  const { channelId, ws } = workspaceOf(run);
  const view = ws.view().side;
  if (!view) throw new StepError(`no side view is open; open_side {view} first`);
  return { channelId, view };
}

function readerOf(run: Run, channelId: string): SideReader {
  const reader = run.host.side(channelId);
  if (!reader) throw new StepError("the side view isn't shown yet");
  return reader;
}

/** How a step reaches its pane: a terminal, a chat (a chat pane, or the
 *  Learn view's learn thread), or the Explain view's text. */
function targetOf(step: UiStep, run: Run): "terminal" | "chat" | "explain" {
  if (onSide(step)) {
    const { view } = sideOf(run);
    return view === "shell" ? "terminal" : view === "learn" ? "chat" : "explain";
  }
  const { ws } = workspaceOf(run);
  return CHAT_PANELS.includes(findPane(ws.view().panes, step.pane).panel) ? "chat" : "terminal";
}

function panelOf(panel: string | undefined): PanelType {
  if (!panel) throw new StepError("panel is required");
  if (panel !== "plugin" && !PANEL_OPTIONS.some((o) => o.panel === panel)) throw new StepError(`unknown panel "${panel}"`);
  return panel as PanelType;
}

function openModeOf(mode: string | undefined): AgentOpenMode | undefined {
  if (mode === undefined) return undefined;
  if (!AGENT_OPEN_MODE_OPTIONS.some((o) => o.mode === mode)) throw new StepError(`unknown open_mode "${mode}"; use resume, fork or fresh`);
  return mode as AgentOpenMode;
}

/** The panel a step makes: a chat forked or started fresh is a side chat,
 *  since the Chat pane resumes the channel's session. */
function stepPanelOf(step: UiStep): PanelType {
  const panel = panelOf(step.panel);
  return panel === "chat" && (step.open_mode === "fork" || step.open_mode === "fresh") ? "chat-side" : panel;
}

/** Runs a step against the controller, turning its Error into the step's. */
function attempt<T>(fn: () => T): T {
  try {
    return fn();
  } catch (err) {
    throw new StepError(err instanceof Error ? err.message : String(err));
  }
}

/** The plugin panel a plugin pane a step makes shows: item names it as
 *  plugin/panel, or as the plugin's id when it has one panel. A channel just
 *  opened is still listing its plugins, so it waits for them. */
async function pluginPane(step: UiStep, run: Run, ws: WorkspaceController, replacing: string | undefined): Promise<PaneOptions["plugin"]> {
  if (step.scope !== undefined) throw new StepError("scope is for playground panes");
  if (!step.item) throw new StepError("item is required for a plugin pane: plugin/panel, or the plugin's id");
  const all = await waitForValue(run, "the plugins to be listed", () => ws.pluginPanes(replacing));
  const found = all.filter((o) => o.key === step.item || o.ref.id === step.item);
  if (found.length === 0) throw new StepError(`no plugin panel "${step.item}"; plugin panels: ${all.map((o) => o.key).join(", ") || "none"}`);
  if (found.length > 1) throw new StepError(`plugin "${step.item}" has several panels; give plugin/panel: ${found.map((o) => o.key).join(", ")}`);
  const [option] = found as [PluginPaneOption];
  if (option.disabled) throw new StepError(`the tab already shows ${option.key}, which shows once`);
  return option.ref;
}

/** How the pane a step makes starts: an agent's open mode, the playground
 *  a playground pane shows, checked against the channel's, or the panel a
 *  plugin pane shows; replacing is the pane a replace_pane step replaces. */
async function paneOptions(step: UiStep, panel: PanelType, channelId: string, run: Run, ws: WorkspaceController, replacing?: string): Promise<PaneOptions> {
  const opts: PaneOptions = {};
  const openMode = openModeOf(step.open_mode);
  if (openMode && panel === "chat-side" && !openModesFor(panel).includes(openMode)) throw new StepError("a chat-side pane is a fork or a fresh session; use fork or fresh");
  if (openMode) opts.openMode = openMode;
  if (panel === "plugin") {
    opts.plugin = await pluginPane(step, run, ws, replacing);
    return opts;
  }
  if (step.item === undefined && step.scope === undefined) return opts;
  if (panel !== "playground") throw new StepError("item and scope are for playground and plugin panes");
  if (!step.item) throw new StepError("item is required with scope");
  if (step.scope !== undefined && step.scope !== "global" && step.scope !== "project") throw new StepError(`unknown scope "${step.scope}"; use global or project`);
  const all = await run.host.playgrounds(channelId);
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
      const tab = ws.createTab(name, step.canvas);
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
      const { channelId, ws } = workspaceOf(run);
      const pane = findPane(ws.view().panes, step.pane);
      const panel = stepPanelOf(step);
      const opts = await paneOptions(step, panel, channelId, run, ws, pane.id);
      stillOn(run);
      return { pane: attempt(() => ws.replacePane(pane.id, panel, opts)) };
    }
    case "add_pane": {
      const { channelId, ws } = workspaceOf(run);
      const panel = stepPanelOf(step);
      const nextTo = step.next_to === undefined ? undefined : findPane(ws.view().panes, step.next_to).id;
      const direction = step.direction ?? "horizontal";
      if (direction !== "horizontal" && direction !== "vertical") throw new StepError(`unknown direction "${direction}"; use horizontal or vertical`);
      const side = step.side ?? "after";
      if (side !== "before" && side !== "after") throw new StepError(`unknown side "${side}"; use before or after`);
      const opts = await paneOptions(step, panel, channelId, run, ws);
      stillOn(run);
      return { pane: attempt(() => ws.addPane(panel, nextTo, direction, side === "before", opts)) };
    }
    case "remove_pane": {
      const { ws } = workspaceOf(run);
      const pane = findPane(ws.view().panes, step.pane);
      attempt(() => ws.removePane(pane.id));
      return {};
    }
    case "maximize_pane": {
      const { ws } = workspaceOf(run);
      const pane = findPane(ws.view().panes, step.pane);
      attempt(() => ws.maximize(pane.id));
      return { pane: pane.id };
    }
    case "restore_pane": {
      const { ws } = workspaceOf(run);
      attempt(() => ws.maximize(null));
      return {};
    }
    case "open_file": {
      const { channelId } = workspaceOf(run);
      if (!step.path) throw new StepError("path is required");
      if (step.line !== undefined && (!Number.isInteger(step.line) || step.line < 1)) throw new StepError("line must be a whole number from 1");
      await host.openFile(channelId, step.path, step.line).catch((err) => {
        throw new StepError(err instanceof Error ? err.message : String(err));
      });
      return {};
    }
    case "open_walkthrough": {
      const { channelId } = workspaceOf(run);
      const id = step.walkthrough_id;
      if (id === undefined) throw new StepError("walkthrough_id is required");
      if (!Number.isInteger(id) || id < 1) throw new StepError("walkthrough_id must be a whole number from 1");
      const n = step.walkthrough_step ?? 1;
      if (!Number.isInteger(n) || n < 1) throw new StepError("walkthrough_step must be a whole number from 1");
      const sec = step.autoplay_sec;
      if (sec !== undefined && (!Number.isInteger(sec) || sec < 1 || sec > MAX_AUTOPLAY_SEC)) throw new StepError(`autoplay_sec must be a whole number from 1 to ${MAX_AUTOPLAY_SEC}`);
      const walkthrough = await host.walkthrough(channelId, id).catch((err) => {
        throw new StepError(err instanceof Error ? err.message : String(err));
      });
      if (!walkthrough) throw new StepError(`no walkthrough ${id} in channel ${channelId}`);
      const count = walkthrough.steps?.length ?? 0;
      if (count === 0) throw new StepError(`walkthrough ${id} has no steps`);
      if (n > count) throw new StepError(`walkthrough ${id} has ${count} step${count === 1 ? "" : "s"}; walkthrough_step must be from 1 to ${count}`);
      stillOn(run);
      await host.showWalkthrough(channelId, walkthrough, n - 1, sec).catch((err) => {
        throw new StepError(err instanceof Error ? err.message : String(err));
      });
      return {};
    }
    case "close_walkthrough": {
      const { channelId } = workspaceOf(run);
      host.closeWalkthrough(channelId);
      return {};
    }
    case "show_message": {
      const { channelId } = workspaceOf(run);
      const id = step.message_id;
      if (id === undefined) throw new StepError("message_id is required");
      if (!Number.isInteger(id) || id < 1) throw new StepError("message_id must be a whole number from 1");
      // The chat scrolls once it has paged back to the message, which can
      // take a while in a long chat: wait for it within the deadline.
      let shown: boolean | null = null;
      let failed: unknown = null;
      host.showMessage(channelId, id).then(
        (found) => {
          shown = found;
        },
        (err: unknown) => {
          failed = err ?? "failed";
        },
      );
      const found = await waitForValue(run, `message ${id} to show`, () => {
        if (failed !== null) throw new StepError(failed instanceof Error ? failed.message : String(failed));
        return shown;
      });
      if (!found) throw new StepError(`no message ${id} in channel ${channelId}'s chat`);
      return {};
    }
    case "open_side": {
      const { ws } = workspaceOf(run);
      const view = sideViewOf(step.view);
      if (ws.view().side !== view) {
        ws.openSide(view);
        await waitFor(run, `the ${SIDE_VIEW_LABEL[view]} to open`, () => ws.view().side === view);
      }
      return { pane: SIDE_PANE };
    }
    case "close_side": {
      const { ws } = workspaceOf(run);
      if (ws.view().side) {
        ws.closeSide();
        await waitFor(run, "the side view to close", () => !ws.view().side);
      }
      return {};
    }
    case "send_input":
      return sendInput(step, run);
    case "read_output": {
      const lines = linesOf(step);
      if (onSide(step)) {
        // The Learn view reads as its proposals and its learn thread.
        const { channelId, view } = sideOf(run);
        if (view !== "shell") return { pane: SIDE_PANE, output: readerOf(run, channelId).read(view, lines) };
      } else if (isChatPane(step, run)) {
        const { pane, chat } = await chatOf(step, run);
        return { pane, output: chatText(chat.messages(), lines) };
      }
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
  let pane = SIDE_PANE;
  if (!onSide(step)) {
    const info = findPane(ws.view().panes, step.pane);
    if (!INPUT_PANELS.includes(info.panel)) throw new StepError(`${step.op} only ${verb} docker-agent, docker-shell, chat and chat-side panes and the side view, not ${info.panel}`);
    pane = info.id;
  }
  // A pane added a step before mounts and starts its session first.
  let found: TerminalInput | undefined;
  await waitFor(run, `pane ${pane} to be ${ready ? "ready for input" : "started"}`, () => {
    found = host.terminal(channelId, pane);
    if (!found) return false;
    const status = found.status();
    if (ready && (status === "completed" || status === "failed")) throw new StepError(`pane ${pane}'s session has ${status === "failed" ? "failed" : "ended"}`);
    return ready ? ready(found) : status !== "connecting";
  });
  return { channelId, pane, t: found as TerminalInput };
}

/** Whether the step's pane is a chat pane. */
function isChatPane(step: UiStep, run: Run): boolean {
  return targetOf(step, run) === "chat";
}

/** The chat of the step's chat pane, once it's there: a chat-side pane
 *  registers its chat once its side chat has started, the Learn view once
 *  its learn thread is shown. */
async function chatOf(step: UiStep, run: Run): Promise<{ channelId: string; pane: string; chat: ChatPaneInput }> {
  const { channelId, ws } = workspaceOf(run);
  const pane = onSide(step) ? SIDE_PANE : findPane(ws.view().panes, step.pane).id;
  const chat = await waitForValue(run, `pane ${pane}'s chat to start`, () => run.host.chat(channelId, pane) ?? null);
  return { channelId, pane, chat };
}

/** The last lines of a chat's messages, each as "author: text", with a
 *  blank line between them, from the first that isn't blank. */
function chatText(messages: ChatLine[], lines: number): string {
  const all = messages
    .map((m) => `${m.author}: ${m.content}`)
    .join("\n\n")
    .split("\n")
    .slice(-lines);
  return all.slice(all.findIndex((l) => l !== "")).join("\n");
}

const lastId = (messages: ChatLine[]) => messages.reduce((max, m) => Math.max(max, m.id), 0);

/** Queues the text as the chat's next message, as its composer sends it. */
async function sendChat(step: UiStep, run: Run, text: string): Promise<StepOutput> {
  if (!text.trim()) throw new StepError("text can't be empty for a chat pane");
  if (step.submit === false) throw new StepError("a chat pane sends what it's given; submit: false is for terminals");
  const { pane, chat } = await chatOf(step, run);
  const blocked = chat.blocked();
  if (blocked) throw new StepError(`pane ${pane}'s chat waits on an answer to ${blocked}; answer it in the window`);
  run.chatSince.set(pane, lastId(chat.messages()));
  await chat.send(text).catch((err) => {
    throw new StepError(err instanceof Error ? err.message : String(err));
  });
  return { pane };
}

/** Waits until the chat has replied to a message send_input queued earlier
 *  in the command and has nothing left to run, or, with match, until its
 *  replies since match (the message sent, which may hold the pattern, never
 *  does); without a send_input, all its replies count and it's done once it
 *  has been idle for quietMs. */
async function waitForChat(step: UiStep, run: Run, match: RE2JS | undefined, quietMs: number, lines: number): Promise<StepOutput> {
  const { host } = run;
  const found = await chatOf(step, run);
  const { channelId, pane } = found;
  let chat = found.chat;
  const since = run.chatSince.get(pane);
  let idleSince: number | null = null;
  await waitFor(run, match ? `pane ${pane}'s chat to reply /${step.match}/` : `pane ${pane}'s chat to reply`, () => {
    // A pane that remounted has a new chat.
    const current = host.chat(channelId, pane);
    if (!current) throw new StepError(`pane ${pane} is no longer open`);
    chat = current;
    const fresh = chat.messages().filter((m) => m.id > (since ?? 0));
    if (match)
      return match.test(
        chatText(
          fresh.filter((m) => m.bot),
          lines,
        ),
      );
    if (chat.busy() || (since !== undefined && !fresh.some((m) => m.bot))) {
      idleSince = null;
      return false;
    }
    idleSince ??= host.now();
    return host.now() - idleSince >= (since === undefined ? quietMs : 0);
  });
  return { pane, output: chatText(chat.messages(), lines) };
}

async function sendInput(step: UiStep, run: Run): Promise<StepOutput> {
  if (typeof step.text !== "string") throw new StepError("text is required");
  const target = targetOf(step, run);
  if (target === "explain") throw new StepError("send_input on the side view types into the Shell view or asks the Learn view's learn thread; the Explain view takes no input");
  if (target === "chat") return sendChat(step, run, step.text);
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
  // RE2 matches in linear time: a backtracking RegExp from a command could
  // take minutes on the window's thread for a pattern like ^(a+)+$.
  let match: RE2JS | undefined;
  if (step.match !== undefined) {
    try {
      match = RE2JS.compile(step.match, RE2JS.MULTILINE);
    } catch (err) {
      throw new StepError(`match: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  const quietMs = step.quiet_ms ?? WAIT_QUIET_MS;
  if (!Number.isInteger(quietMs) || quietMs < 0) throw new StepError("quiet_ms must be a whole number from 0");
  const target = targetOf(step, run);
  if (target === "explain") return waitForExplain(step, run, match, quietMs, lines);
  if (target === "chat") return waitForChat(step, run, match, quietMs, lines);
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

/** Waits until the Explain view's text matches, or, without match, until no
 *  explanation has been running for quietMs. */
async function waitForExplain(step: UiStep, run: Run, match: RE2JS | undefined, quietMs: number, lines: number): Promise<StepOutput> {
  const { host } = run;
  const { channelId } = sideOf(run);
  const reader = readerOf(run, channelId);
  let idleSince: number | null = null;
  await waitFor(run, match ? `the Explain view to show /${step.match}/` : "the Explain view to be idle", () => {
    if (sideOf(run).view !== "explain") throw new StepError("the Explain view is no longer open");
    if (match) return match.test(reader.read("explain", lines));
    if (reader.explaining()) {
      idleSince = null;
      return false;
    }
    idleSince ??= host.now();
    return host.now() - idleSince >= quietMs;
  });
  return { pane: SIDE_PANE, output: reader.read("explain", lines) };
}
