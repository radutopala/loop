import type { TerminalInput, WorkspaceController } from "./types";

// Where the mounted workspace and its agent terminals make themselves
// reachable to UI commands, and say when what a window reports changed.

const workspaces = new Map<string, WorkspaceController>();
const terminals = new Map<string, TerminalInput>();
const listeners = new Set<() => void>();

/** Registers the workspace of channelId; the returned function unregisters
 *  it, unless another has registered since. */
export function registerWorkspace(channelId: string, controller: WorkspaceController): () => void {
  workspaces.set(channelId, controller);
  notifyUiChanged();
  return () => {
    if (workspaces.get(channelId) !== controller) return;
    workspaces.delete(channelId);
    notifyUiChanged();
  };
}

export function getWorkspace(channelId: string): WorkspaceController | undefined {
  return workspaces.get(channelId);
}

const terminalKey = (channelId: string, paneId: string) => `${channelId}:${paneId}`;

/** Registers an agent terminal's input; the returned function unregisters
 *  it, unless another has registered since. */
export function registerTerminalInput(channelId: string, paneId: string, input: TerminalInput): () => void {
  const key = terminalKey(channelId, paneId);
  terminals.set(key, input);
  return () => {
    if (terminals.get(key) === input) terminals.delete(key);
  };
}

export function getTerminalInput(channelId: string, paneId: string): TerminalInput | undefined {
  return terminals.get(terminalKey(channelId, paneId));
}

/** Says the open channel, its tabs or its panes changed. */
export function notifyUiChanged(): void {
  for (const fn of listeners) fn();
}

export function subscribeUiChanged(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
