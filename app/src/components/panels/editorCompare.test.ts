import { getChunks } from "@codemirror/merge";
import { EditorState } from "@codemirror/state";
import { afterEach, describe, expect, it, vi } from "vitest";
import { compareChunkButton, compareExtension } from "./editorCompare";

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
    ["reject", "Use disk"],
    ["accept", "Keep mine"],
  ] as const)("labels the %s button %s and runs its action on mouse down", (type, label) => {
    vi.stubGlobal("document", { createElement: () => ({ dataset: {} }) });
    const action = vi.fn();
    const button = compareChunkButton(type, action);
    expect(button.textContent).toBe(label);
    expect(button.dataset.testid).toBe(`compare-${type}`);
    expect((button as HTMLButtonElement).name).toBe(type);
    const e = {} as MouseEvent;
    button.onmousedown?.call(button, e);
    expect(action).toHaveBeenCalledWith(e);
  });
});
