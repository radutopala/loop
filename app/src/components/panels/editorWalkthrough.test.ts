// @vitest-environment jsdom
import { EditorSelection, EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { describe, expect, it, vi } from "vitest";
import { MAX_SELECTED_CHARS, selectedRange, setWalkthroughCard, shownWalkthroughRange, type WalkthroughCard, walkthroughExtension } from "./editorWalkthrough";

const noop = () => {};
const handlers = { prev: noop, next: noop, close: noop, ask: noop, togglePlay: noop, pause: noop };

const doc = ["package main", "", "func a() {", "\treturn", "}"].join("\n");

const card = (start: number, end: number, anchor?: string): WalkthroughCard => ({
  walkthroughTitle: "Tour",
  index: 0,
  count: 2,
  step: { path: "main.go", start_line: start, end_line: end, anchor },
});

const stateWith = (text: string, c: WalkthroughCard | null) => EditorState.create({ doc: text, extensions: walkthroughExtension(handlers) }).update({ effects: setWalkthroughCard.of(c) }).state;

describe("the walkthrough layer", () => {
  it("shows the step's lines where its anchor is now", () => {
    const state = stateWith(`// new\n${doc}`, card(3, 5, "func a() {\n\treturn\n}"));
    expect(shownWalkthroughRange(state)).toEqual({ start_line: 4, end_line: 6, text: "func a() {\n\treturn\n}" });
  });

  it("shows the step's lines where they were when its anchor is gone", () => {
    expect(shownWalkthroughRange(stateWith(doc, card(1, 2, "gone")))).toEqual({ start_line: 1, end_line: 2, text: "package main\n" });
  });

  it("moves the step's lines with edits above them", () => {
    const state = stateWith(doc, card(3, 5));
    const edited = state.update({ changes: { from: 0, insert: "// one\n// two\n" } }).state;
    expect(shownWalkthroughRange(edited)?.start_line).toBe(5);
    expect(shownWalkthroughRange(edited)?.end_line).toBe(7);
  });

  it("shows none once cleared, or without the layer", () => {
    const state = stateWith(doc, card(3, 5));
    expect(shownWalkthroughRange(state.update({ effects: setWalkthroughCard.of(null) }).state)).toBeNull();
    expect(shownWalkthroughRange(EditorState.create({ doc }))).toBeNull();
  });
});

describe("selectedRange", () => {
  const select = (from: number, to: number, text = doc) => EditorState.create({ doc: text, selection: EditorSelection.single(from, to) });

  it("gives the selection with its lines", () => {
    expect(selectedRange(select(14, 30))).toEqual({ start_line: 3, end_line: 4, text: doc.slice(14, 30) });
  });

  it("gives null for an empty selection", () => {
    expect(selectedRange(select(3, 3))).toBeNull();
  });

  it("cuts long text short", () => {
    const long = "z".repeat(MAX_SELECTED_CHARS * 2);
    expect(selectedRange(select(0, long.length, long))?.text).toHaveLength(MAX_SELECTED_CHARS);
  });
});

describe("the walkthrough card", () => {
  const mount = (c: WalkthroughCard) => {
    const h = { ...handlers, togglePlay: vi.fn(), pause: vi.fn() };
    const view = new EditorView({ doc, extensions: walkthroughExtension(h), parent: document.body });
    view.dispatch({ effects: setWalkthroughCard.of(c) });
    const buttons = () => Array.from(view.dom.querySelectorAll<HTMLButtonElement>(".cm-walkthrough-btn"));
    const named = (label: string) => buttons().find((b) => b.textContent === label);
    return { h, view, buttons, named };
  };

  it("plays and pauses", () => {
    const { h, view, named } = mount(card(3, 5));
    expect(named("Pause")).toBeUndefined();
    named("Play")?.click();
    expect(h.togglePlay).toHaveBeenCalledTimes(1);
    view.dispatch({ effects: setWalkthroughCard.of({ ...card(3, 5), playing: true }) });
    expect(named("Play")).toBeUndefined();
    named("Pause")?.click();
    expect(h.togglePlay).toHaveBeenCalledTimes(2);
    view.destroy();
  });

  it("can't play a walkthrough of one step", () => {
    const { view, named } = mount({ ...card(3, 5), count: 1 });
    expect(named("Play")?.disabled).toBe(true);
    view.destroy();
  });

  it("pauses when the user edits the file, not when it's reloaded", () => {
    const { h, view } = mount(card(3, 5));
    view.dispatch({ changes: { from: 0, insert: "// reloaded\n" } });
    expect(h.pause).not.toHaveBeenCalled();
    view.dispatch({ changes: { from: 0, insert: "x" }, userEvent: "input.type" });
    expect(h.pause).toHaveBeenCalledTimes(1);
    view.dispatch({ effects: setWalkthroughCard.of(null) });
    view.dispatch({ changes: { from: 0, insert: "y" }, userEvent: "input.type" });
    expect(h.pause).toHaveBeenCalledTimes(1);
    view.destroy();
  });
});
