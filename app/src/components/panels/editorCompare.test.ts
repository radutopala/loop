import { getChunks } from "@codemirror/merge";
import { EditorState } from "@codemirror/state";
import { afterEach, describe, expect, it, vi } from "vitest";
import { compareChunkButton, compareExtension, minimalChange } from "./editorCompare";

describe("compareExtension", () => {
  it.each([null, undefined])("adds nothing when there is nothing to compare with (%s)", (original) => {
    expect(compareExtension(original)).toEqual([]);
  });

  it("finds the changes against the text on disk", () => {
    const state = EditorState.create({ doc: "one\nmine\nthree\n", extensions: compareExtension("one\ntwo\nthree\n") });
    const [chunk, ...rest] = getChunks(state)?.chunks ?? [];
    expect(rest).toHaveLength(0);
    expect(chunk && state.doc.sliceString(chunk.fromB, chunk.toB)).toBe("mine\n");
  });

  it("finds no changes when the texts match", () => {
    const state = EditorState.create({ doc: "same\n", extensions: compareExtension("same\n") });
    expect(getChunks(state)?.chunks).toHaveLength(0);
  });
});

describe("compareChunkButton", () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each([
    ["reject", "disk", "Use disk"],
    ["accept", "disk", "Keep mine"],
    ["reject", "head", "Revert"],
    ["accept", "head", "Keep"],
  ] as const)("labels the %s button against %s %s and runs its action on mouse down", (type, source, label) => {
    vi.stubGlobal("document", { createElement: () => ({ dataset: {} }) });
    const action = vi.fn();
    const button = compareChunkButton(type, action, source);
    expect(button.textContent).toBe(label);
    expect(button.dataset.testid).toBe(`compare-${type}`);
    expect((button as HTMLButtonElement).name).toBe(type);
    const e = {} as MouseEvent;
    button.onmousedown?.call(button, e);
    expect(action).toHaveBeenCalledWith(e);
  });
});

describe("minimalChange", () => {
  it.each([
    ["same", "same", null],
    ["abc", "aXc", { from: 1, to: 2, insert: "X" }],
    ["abc", "abc\n", { from: 3, to: 3, insert: "\n" }],
    ["a  b", "a b", { from: 2, to: 3, insert: "" }],
    ["", "new", { from: 0, to: 0, insert: "new" }],
    ["aaa", "aa", { from: 2, to: 3, insert: "" }],
  ])("turns %j into %j", (current, next, want) => {
    const change = minimalChange(current, next);
    expect(change).toEqual(want);
    if (change) expect(current.slice(0, change.from) + change.insert + current.slice(change.to)).toBe(next);
  });
});
