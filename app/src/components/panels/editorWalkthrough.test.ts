// @vitest-environment jsdom
import { EditorSelection, EditorState, StateEffect } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { describe, expect, it, vi } from "vitest";
import { darkColors } from "../../theme";
import { MAX_SELECTED_CHARS, selectedRange, setWalkthroughCard, shownWalkthroughRange, type WalkthroughCard, walkthroughExtension, walkthroughTheme } from "./editorWalkthrough";

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

  it("keeps the step through transactions with other effects", () => {
    const state = stateWith(doc, card(3, 5));
    const other = StateEffect.define<number>();
    expect(shownWalkthroughRange(state.update({ effects: other.of(1) }).state)?.start_line).toBe(3);
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
    const h = { prev: vi.fn(), next: vi.fn(), close: vi.fn(), ask: vi.fn(), togglePlay: vi.fn(), pause: vi.fn() };
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

  const press = (view: EditorView, key: string) => {
    const event = new KeyboardEvent("keydown", { key, altKey: key.startsWith("Arrow"), bubbles: true, cancelable: true });
    view.contentDOM.dispatchEvent(event);
    return event.defaultPrevented;
  };

  it("shows the step's title, body, why it changed and the notice", () => {
    const c: WalkthroughCard = { ...card(3, 5, "not in the file"), step: { ...card(3, 5).step, anchor: "not in the file", title: "Entry", body: "Runs **first**" }, notice: "Step 3 is gone" };
    const { view } = mount(c);
    const root = view.dom.querySelector<HTMLElement>('[data-testid="walkthrough-card"]')!;
    expect(root.querySelector(".cm-walkthrough-title")?.textContent).toBe("Tour");
    expect(root.querySelector(".cm-walkthrough-count")?.textContent).toBe("Step 1 of 2");
    expect(root.querySelector(".cm-walkthrough-stepTitle")?.textContent).toBe("Entry");
    expect(root.querySelector(".cm-walkthrough-body strong")?.textContent).toBe("first");
    expect(Array.from(root.querySelectorAll(".cm-walkthrough-note"), (n) => n.textContent)).toEqual([
      "This code changed since the walkthrough was made; the step shows where it was.",
      "Step 3 is gone",
    ]);
    view.destroy();
  });

  it("leaves out the title, body and notes a step doesn't have", () => {
    const { view } = mount(card(3, 5));
    expect(view.dom.querySelector(".cm-walkthrough-stepTitle")).toBeNull();
    expect(view.dom.querySelector(".cm-walkthrough-body")).toBeNull();
    expect(view.dom.querySelector(".cm-walkthrough-note")).toBeNull();
    view.destroy();
  });

  it("redraws the card when the same step's code changes", () => {
    const c = card(3, 5, "func a() {\n\treturn\n}");
    const { view } = mount(c);
    expect(view.dom.querySelector(".cm-walkthrough-note")).toBeNull();
    view.dispatch({ changes: { from: 14, to: 24, insert: "func b() {" } });
    view.dispatch({ effects: setWalkthroughCard.of(c) });
    expect(view.dom.querySelector(".cm-walkthrough-note")?.textContent).toMatch(/This code changed/);
    view.destroy();
  });

  it("runs Prev, Next, Close and Ask with the step's current lines", () => {
    const { h, view, named } = mount({ ...card(3, 5), index: 1, count: 3 });
    named("Prev")?.click();
    named("Next")?.click();
    named("Close")?.click();
    named("Ask about this step")?.click();
    expect(h.prev).toHaveBeenCalledTimes(1);
    expect(h.next).toHaveBeenCalledTimes(1);
    expect(h.close).toHaveBeenCalledTimes(1);
    expect(h.ask).toHaveBeenCalledWith({ start_line: 3, end_line: 5, text: "func a() {\n\treturn\n}" });
    view.destroy();
  });

  it("disables Prev on the first step and Next on the last", () => {
    const first = mount(card(3, 5));
    expect(first.named("Prev")?.disabled).toBe(true);
    expect(first.named("Next")?.disabled).toBe(false);
    first.view.destroy();
    const last = mount({ ...card(3, 5), index: 1 });
    expect(last.named("Prev")?.disabled).toBe(false);
    expect(last.named("Next")?.disabled).toBe(true);
    last.view.destroy();
  });

  it("asks nothing from a card whose step was cleared", () => {
    const { h, view, named } = mount(card(3, 5));
    const ask = named("Ask about this step")!;
    view.dispatch({ effects: setWalkthroughCard.of(null) });
    ask.click();
    expect(h.ask).not.toHaveBeenCalled();
    view.destroy();
  });

  it("keeps the card's own events from the editor", () => {
    const { view } = mount(card(3, 5));
    const root = view.dom.querySelector<HTMLElement>('[data-testid="walkthrough-card"]')!;
    const select = vi.spyOn(view, "dispatch");
    root.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 }));
    expect(select).not.toHaveBeenCalled();
    view.destroy();
  });

  it("binds Alt+arrows and Esc while a step shows, and lets them through otherwise", () => {
    const { h, view } = mount(card(3, 5));
    expect(press(view, "ArrowLeft")).toBe(true);
    expect(press(view, "ArrowRight")).toBe(true);
    expect(press(view, "Escape")).toBe(true);
    expect(h.prev).toHaveBeenCalledTimes(1);
    expect(h.next).toHaveBeenCalledTimes(1);
    expect(h.close).toHaveBeenCalledTimes(1);
    view.dispatch({ effects: setWalkthroughCard.of(null) });
    expect(press(view, "Escape")).toBe(false);
    expect(h.close).toHaveBeenCalledTimes(1);
    view.destroy();
  });
});

describe("walkthroughTheme", () => {
  it("styles the step's lines with the palette's accent", () => {
    const view = new EditorView({ doc, extensions: walkthroughTheme(darkColors), parent: document.body });
    const css = Array.from(document.querySelectorAll("style"), (s) => s.textContent).join("\n");
    expect(css).toContain(`inset 3px 0 0 ${darkColors.active}`);
    view.destroy();
  });
});
