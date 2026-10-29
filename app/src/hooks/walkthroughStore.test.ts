import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WalkthroughStep } from "../api/walkthroughs";
import { subscribeUiChanged } from "../uiBridge/registry";
import type { FileLinkTarget } from "../utils/fileLinks";
import {
  closeWalkthrough,
  DEFAULT_AUTOPLAY_SEC,
  editorUiState,
  getEditorSelection,
  getOpenWalkthrough,
  goToStep,
  MAX_SELECTION_CHARS,
  pauseWalkthrough,
  playWalkthrough,
  setEditorSelection,
  showWalkthrough,
  subscribeWalkthroughs,
} from "./walkthroughStore";

const steps: WalkthroughStep[] = [
  { path: "a.go", start_line: 1, end_line: 2, title: "A" },
  { path: "/abs/b.go", start_line: 5, end_line: 9 },
  { path: "gone.go", start_line: 1, end_line: 1 },
];
const walkthrough = { id: 12, title: "Tour", steps };

/** Opens files it knows, as the editor does; the others reject. */
class FakeFiles {
  opened: string[] = [];
  known: Record<string, FileLinkTarget> = { "a.go": { rootIndex: 0, relPath: "a.go" }, "/abs/b.go": { rootIndex: 1, relPath: "b.go" } };
  open = async (channelId: string, path: string) => {
    const target = this.known[path];
    if (!target) throw new Error(`no file "${path}" in the channel's roots`);
    this.opened.push(`${channelId} ${path}`);
    return target;
  };
}

let files: FakeFiles;
let notified: number;
let uiChanged: number;
let unsubscribe: (() => void)[];

beforeEach(() => {
  files = new FakeFiles();
  notified = 0;
  uiChanged = 0;
  unsubscribe = [subscribeWalkthroughs(() => notified++), subscribeUiChanged(() => uiChanged++)];
});

afterEach(() => {
  for (const fn of unsubscribe) fn();
  for (const ch of ["c1", "c2"]) {
    closeWalkthrough(ch);
    setEditorSelection(ch, null);
  }
});

describe("showWalkthrough", () => {
  it("opens the step's file and shows the step", async () => {
    await showWalkthrough("c1", walkthrough, 1, files.open);
    expect(files.opened).toEqual(["c1 /abs/b.go"]);
    expect(getOpenWalkthrough("c1")).toEqual({ id: 12, title: "Tour", steps, step: 1, target: { rootIndex: 1, relPath: "b.go" } });
    expect(getOpenWalkthrough("c2")).toBeUndefined();
    expect(notified).toBe(1);
    expect(uiChanged).toBe(1);
  });

  it("rejects a step it doesn't have", async () => {
    await expect(showWalkthrough("c1", walkthrough, 3, files.open)).rejects.toThrow("walkthrough 12 has no step 4");
    expect(getOpenWalkthrough("c1")).toBeUndefined();
  });

  it("rejects a step whose file is gone, leaving the walkthrough shown", async () => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    await expect(showWalkthrough("c1", walkthrough, 2, files.open)).rejects.toThrow(`no file "gone.go" in the channel's roots`);
    expect(getOpenWalkthrough("c1")?.step).toBe(0);
  });

  it("lets the show asked for last win", async () => {
    let release: () => void = () => {};
    const slow = (channelId: string, path: string) =>
      new Promise<FileLinkTarget>((resolve) => {
        release = () => void files.open(channelId, path).then(resolve);
      });
    const first = showWalkthrough("c1", walkthrough, 0, slow);
    await showWalkthrough("c1", walkthrough, 1, files.open);
    release();
    await first;
    expect(getOpenWalkthrough("c1")?.step).toBe(1);
  });
});

describe("goToStep", () => {
  it("moves the open walkthrough to the step", async () => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    await goToStep("c1", 1, files.open);
    expect(getOpenWalkthrough("c1")).toMatchObject({ step: 1, target: { rootIndex: 1, relPath: "b.go" } });
  });

  it("stays on the step it shows, saying why, when the step's file is gone", async () => {
    await showWalkthrough("c1", walkthrough, 1, files.open);
    await goToStep("c1", 2, files.open);
    expect(getOpenWalkthrough("c1")).toMatchObject({ step: 1, notice: `Step 3: no file "gone.go" in the channel's roots` });
    // Showing a step clears it.
    await goToStep("c1", 0, files.open);
    expect(getOpenWalkthrough("c1")?.notice).toBeUndefined();
  });

  it.each([-1, 3])("does nothing for step %s", async (index) => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    await goToStep("c1", index, files.open);
    expect(files.opened).toEqual(["c1 a.go"]);
  });

  it("does nothing without a walkthrough", async () => {
    await goToStep("c1", 0, files.open);
    expect(files.opened).toEqual([]);
    expect(getOpenWalkthrough("c1")).toBeUndefined();
  });

  it("leaves a walkthrough closed while its step opened closed", async () => {
    await showWalkthrough("c1", walkthrough, 1, files.open);
    let reject: (err: Error) => void = () => {};
    const going = goToStep("c1", 0, () => new Promise<FileLinkTarget>((_, r) => (reject = r)));
    closeWalkthrough("c1");
    reject(new Error("gone"));
    await going;
    expect(getOpenWalkthrough("c1")).toBeUndefined();
  });
});

describe("closeWalkthrough", () => {
  it("closes the walkthrough, and drops a show still opening its file", async () => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    let release: () => void = () => {};
    const slow = () => new Promise<FileLinkTarget>((resolve) => (release = () => resolve({ rootIndex: 0, relPath: "a.go" })));
    const showing = showWalkthrough("c1", walkthrough, 1, slow);
    closeWalkthrough("c1");
    release();
    await showing;
    expect(getOpenWalkthrough("c1")).toBeUndefined();
  });

  it("says nothing changed when there's none", () => {
    closeWalkthrough("c1");
    expect(notified).toBe(0);
  });
});

describe("setEditorSelection", () => {
  const sel = { path: "/root/a.go", start_line: 2, end_line: 3, text: "x := 1\ny := 2" };

  it("keeps the selection, saying so once per change", () => {
    setEditorSelection("c1", sel);
    setEditorSelection("c1", { ...sel });
    expect(getEditorSelection("c1")).toEqual(sel);
    expect(uiChanged).toBe(1);
    setEditorSelection("c1", { ...sel, end_line: 4 });
    expect(uiChanged).toBe(2);
  });

  it("clears it with null or an empty selection", () => {
    setEditorSelection("c1", sel);
    setEditorSelection("c1", { ...sel, text: "" });
    expect(getEditorSelection("c1")).toBeUndefined();
    setEditorSelection("c1", null);
    expect(uiChanged).toBe(2);
  });

  it("cuts its text short", () => {
    setEditorSelection("c1", { ...sel, text: "x".repeat(MAX_SELECTION_CHARS + 10) });
    expect(getEditorSelection("c1")?.text).toHaveLength(MAX_SELECTION_CHARS);
  });
});

describe("editorUiState", () => {
  it("reports the walkthrough, its step from 1, and the selection", async () => {
    await showWalkthrough("c1", walkthrough, 1, files.open);
    setEditorSelection("c1", { path: "/root/a.go", start_line: 1, end_line: 1, text: "package main" });
    expect(editorUiState("c1")).toEqual({
      walkthrough: { id: 12, title: "Tour", step: 2, steps: 3 },
      selection: { path: "/root/a.go", start_line: 1, end_line: 1, text: "package main" },
    });
  });

  it("reports neither when there's none", () => {
    expect(editorUiState("c1")).toEqual({});
  });
});

describe("playWalkthrough", () => {
  const two = { id: 12, title: "Tour", steps: steps.slice(0, 2) };

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("moves to the next step at its pace and stops on the last", async () => {
    await showWalkthrough("c1", two, 0, files.open);
    playWalkthrough("c1", files.open, 5);
    expect(editorUiState("c1").walkthrough).toEqual({ id: 12, title: "Tour", step: 1, steps: 2, playing: true, autoplay_sec: 5 });
    await vi.advanceTimersByTimeAsync(4999);
    expect(getOpenWalkthrough("c1")?.step).toBe(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(getOpenWalkthrough("c1")).toMatchObject({ step: 1, playing: false, autoplaySec: 5 });
    expect(files.opened).toEqual(["c1 a.go", "c1 /abs/b.go"]);
    expect(editorUiState("c1").walkthrough).toEqual({ id: 12, title: "Tour", step: 2, steps: 2 });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("plays at the pace it last played at, or the default", async () => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    playWalkthrough("c1", files.open);
    expect(getOpenWalkthrough("c1")?.autoplaySec).toBe(DEFAULT_AUTOPLAY_SEC);
    pauseWalkthrough("c1");
    playWalkthrough("c1", files.open, 3);
    pauseWalkthrough("c1");
    playWalkthrough("c1", files.open);
    expect(getOpenWalkthrough("c1")?.autoplaySec).toBe(3);
  });

  it("stays paused on the last step", async () => {
    await showWalkthrough("c1", two, 1, files.open);
    const before = notified;
    playWalkthrough("c1", files.open, 5);
    expect(getOpenWalkthrough("c1")?.playing).toBe(false);
    expect(notified).toBe(before + 1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does nothing without a walkthrough", () => {
    playWalkthrough("c1", files.open, 5);
    pauseWalkthrough("c1");
    expect(getOpenWalkthrough("c1")).toBeUndefined();
    expect(notified).toBe(0);
  });

  it("pauses", async () => {
    await showWalkthrough("c1", two, 0, files.open);
    playWalkthrough("c1", files.open, 5);
    const before = notified;
    pauseWalkthrough("c1");
    pauseWalkthrough("c1");
    expect(notified).toBe(before + 1);
    expect(getOpenWalkthrough("c1")?.playing).toBe(false);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(getOpenWalkthrough("c1")?.step).toBe(0);
  });

  it("stops at a step whose file can't be opened", async () => {
    await showWalkthrough("c1", { ...walkthrough, steps: [steps[0], steps[2]] as WalkthroughStep[] }, 0, files.open);
    playWalkthrough("c1", files.open, 1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(getOpenWalkthrough("c1")).toMatchObject({ step: 0, playing: false, notice: 'Step 2: no file "gone.go" in the channel\'s roots' });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("keeps playing across steps of the same walkthrough, not into another", async () => {
    await showWalkthrough("c1", walkthrough, 0, files.open);
    playWalkthrough("c1", files.open, 5);
    await showWalkthrough("c1", walkthrough, 1, files.open);
    expect(getOpenWalkthrough("c1")).toMatchObject({ step: 1, playing: true, autoplaySec: 5 });
    await showWalkthrough("c1", { ...walkthrough, id: 13 }, 0, files.open);
    expect(getOpenWalkthrough("c1")?.playing).toBeUndefined();
    expect(getOpenWalkthrough("c1")?.autoplaySec).toBeUndefined();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stops when it's closed", async () => {
    await showWalkthrough("c1", two, 0, files.open);
    playWalkthrough("c1", files.open, 5);
    closeWalkthrough("c1");
    expect(vi.getTimerCount()).toBe(0);
  });
});
