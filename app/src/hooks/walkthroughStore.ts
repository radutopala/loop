import { useSyncExternalStore } from "react";
import type { WalkthroughStep } from "../api/walkthroughs";
import { notifyUiChanged } from "../uiBridge/registry";
import type { FileLinkTarget } from "../utils/fileLinks";

// The walkthrough each channel's editor shows, and the editor's selection:
// what the window reports of its editor (ui_state), kept per channel so the
// editor shows a step once its file is the one open.

/** A walkthrough open in a channel's editor, at one of its steps. */
export interface OpenWalkthrough {
  id: number;
  title: string;
  steps: WalkthroughStep[];
  /** The step shown, from 0. */
  step: number;
  /** The step's file, as the editor opened it. */
  target: FileLinkTarget;
  /** Why the step last asked for isn't the one shown. */
  notice?: string;
  /** How long a step shows while it plays, in seconds; unset until it's
   *  played. */
  autoplaySec?: number;
  /** Whether it moves to the next step on its own. */
  playing?: boolean;
}

/** The editor's selection, as ui_state reports it. */
export interface EditorSelection {
  /** The file, as open_file takes it back. */
  path: string;
  /** 1-based, inclusive. */
  start_line: number;
  end_line: number;
  text: string;
}

/** Opens a step's file in the channel's editor and returns where it is;
 *  rejects when there's no such file. */
export type OpenStepFile = (channelId: string, path: string) => Promise<FileLinkTarget>;

/** How much of a selection ui_state reports. */
export const MAX_SELECTION_CHARS = 4000;

/** How long a step shows when the user plays a walkthrough no agent gave a
 *  pace, in seconds. */
export const DEFAULT_AUTOPLAY_SEC = 10;

const open = new Map<string, OpenWalkthrough>();
const selections = new Map<string, EditorSelection>();
// The last show asked for in each channel: an earlier one that resolves
// later doesn't replace it.
const shows = new Map<string, number>();
// The timer moving each playing walkthrough to its next step, and what it
// opens the step's file with.
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const players = new Map<string, OpenStepFile>();
const listeners = new Set<() => void>();

function changed(): void {
  for (const fn of listeners) fn();
  notifyUiChanged();
}

export function subscribeWalkthroughs(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function getOpenWalkthrough(channelId: string): OpenWalkthrough | undefined {
  return open.get(channelId);
}

/** The walkthrough open in the channel's editor, or null. */
export function useOpenWalkthrough(channelId: string): OpenWalkthrough | null {
  return useSyncExternalStore(subscribeWalkthroughs, () => open.get(channelId) ?? null);
}

/**
 * showWalkthrough opens step index (from 0) of the walkthrough in the
 * channel's editor: its file, then the step over it. It rejects when the
 * step's file can't be opened, leaving the editor's walkthrough as it was.
 */
export async function showWalkthrough(channelId: string, walkthrough: { id: number; title: string; steps: WalkthroughStep[] }, index: number, openFile: OpenStepFile): Promise<void> {
  const step = walkthrough.steps[index];
  if (!step) throw new Error(`walkthrough ${walkthrough.id} has no step ${index + 1}`);
  const show = (shows.get(channelId) ?? 0) + 1;
  shows.set(channelId, show);
  const target = await openFile(channelId, step.path);
  if (shows.get(channelId) !== show) return;
  // Moving within the walkthrough keeps its pace and whether it plays.
  const prev = open.get(channelId);
  const pace = prev?.id === walkthrough.id ? { autoplaySec: prev.autoplaySec, playing: prev.playing } : {};
  open.set(channelId, { id: walkthrough.id, title: walkthrough.title, steps: walkthrough.steps, step: index, target, ...pace });
  schedule(channelId);
  changed();
}

/** goToStep moves the channel's walkthrough to step index (from 0), as Prev
 *  and Next do. A step whose file can't be opened leaves it on the step it
 *  shows, saying why. */
export async function goToStep(channelId: string, index: number, openFile: OpenStepFile): Promise<void> {
  const current = open.get(channelId);
  if (!current || index < 0 || index >= current.steps.length) return;
  try {
    await showWalkthrough(channelId, current, index, openFile);
  } catch (err) {
    const now = open.get(channelId);
    if (now?.id !== current.id) return;
    open.set(channelId, { ...now, playing: false, notice: `Step ${index + 1}: ${err instanceof Error ? err.message : String(err)}` });
    schedule(channelId);
    changed();
  }
}

/** schedule sets the timer of the channel's walkthrough: the next step after
 *  its pace while it plays, none otherwise. One playing on its last step
 *  stops. */
function schedule(channelId: string): void {
  clearTimeout(timers.get(channelId));
  timers.delete(channelId);
  const w = open.get(channelId);
  const openFile = players.get(channelId);
  if (!w?.playing || !w.autoplaySec || !openFile) return;
  if (w.step >= w.steps.length - 1) {
    open.set(channelId, { ...w, playing: false });
    return;
  }
  timers.set(
    channelId,
    setTimeout(() => {
      timers.delete(channelId);
      void goToStep(channelId, w.step + 1, openFile);
    }, w.autoplaySec * 1000),
  );
}

/** playWalkthrough plays the channel's walkthrough from the step it shows,
 *  every sec seconds (default the pace it last played at, or
 *  DEFAULT_AUTOPLAY_SEC). On its last step, it stays paused. */
export function playWalkthrough(channelId: string, openFile: OpenStepFile, sec?: number): void {
  const w = open.get(channelId);
  if (!w) return;
  players.set(channelId, openFile);
  open.set(channelId, { ...w, playing: true, autoplaySec: sec ?? w.autoplaySec ?? DEFAULT_AUTOPLAY_SEC });
  schedule(channelId);
  changed();
}

/** pauseWalkthrough stops the channel's walkthrough moving on its own. */
export function pauseWalkthrough(channelId: string): void {
  const w = open.get(channelId);
  if (!w?.playing) return;
  open.set(channelId, { ...w, playing: false });
  schedule(channelId);
  changed();
}

/** closeWalkthrough closes the channel's walkthrough, and drops a show of
 *  one still opening its file. */
export function closeWalkthrough(channelId: string): void {
  shows.set(channelId, (shows.get(channelId) ?? 0) + 1);
  const had = open.delete(channelId);
  players.delete(channelId);
  schedule(channelId);
  if (had) changed();
}

/** setEditorSelection sets the channel's editor selection; null, or an empty
 *  one, clears it. Its text is cut to MAX_SELECTION_CHARS. */
export function setEditorSelection(channelId: string, selection: EditorSelection | null): void {
  const next = selection?.text ? { ...selection, text: selection.text.slice(0, MAX_SELECTION_CHARS) } : null;
  const prev = selections.get(channelId);
  if (!next) {
    if (selections.delete(channelId)) changed();
    return;
  }
  if (prev && prev.path === next.path && prev.start_line === next.start_line && prev.end_line === next.end_line && prev.text === next.text) return;
  selections.set(channelId, next);
  changed();
}

export function getEditorSelection(channelId: string): EditorSelection | undefined {
  return selections.get(channelId);
}

/** What ui_state reports of the channel's editor: the walkthrough it shows
 *  (its step from 1) and its selection, each left out when there's none. */
export function editorUiState(channelId: string): {
  walkthrough?: { id: number; title: string; step: number; steps: number; playing?: boolean; autoplay_sec?: number };
  selection?: EditorSelection;
} {
  const w = open.get(channelId);
  const selection = selections.get(channelId);
  return {
    ...(w ? { walkthrough: { id: w.id, title: w.title, step: w.step + 1, steps: w.steps.length, ...(w.playing ? { playing: true, autoplay_sec: w.autoplaySec } : {}) } } : {}),
    ...(selection ? { selection } : {}),
  };
}
