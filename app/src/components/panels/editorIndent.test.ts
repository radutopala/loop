import { indentUnit } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { describe, expect, it } from "vitest";
import { indentExtension } from "./editorIndent";

describe("indentExtension", () => {
  it.each([
    { name: "spaces", tabSize: 2, insertSpaces: true, size: 2, unit: "  " },
    { name: "tabs", tabSize: 8, insertSpaces: false, size: 8, unit: "\t" },
    { name: "out of range falls back to 4", tabSize: 0, insertSpaces: true, size: 4, unit: "    " },
    { name: "a fraction falls back to 4", tabSize: 2.5, insertSpaces: false, size: 4, unit: "\t" },
    { name: "too wide falls back to 4", tabSize: 17, insertSpaces: true, size: 4, unit: "    " },
  ])("$name", ({ tabSize, insertSpaces, size, unit }) => {
    const state = EditorState.create({ extensions: indentExtension(tabSize, insertSpaces) });
    expect(state.tabSize).toBe(size);
    expect(state.facet(indentUnit)).toBe(unit);
  });
});
