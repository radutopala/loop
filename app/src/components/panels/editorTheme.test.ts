// @vitest-environment jsdom
import { EditorView } from "@codemirror/view";
import { afterEach, describe, expect, it } from "vitest";
import { darkColors, lightColors } from "../../theme";
import { gitGutterColors } from "./editorGitGutter";
import { buildEditorTheme } from "./editorTheme";

const css = () => Array.from(document.querySelectorAll("style"), (s) => s.textContent).join("\n");

describe("buildEditorTheme", () => {
  let view: EditorView | undefined;
  afterEach(() => view?.destroy());

  it.each([
    ["dark", darkColors, "#a9b7c6", "#cc7832"],
    ["light", lightColors, "#080808", "#0033b3"],
  ])("styles the %s editor with its text, keyword and gutter colours", (_name, palette, text, keyword) => {
    view = new EditorView({ doc: "x", extensions: buildEditorTheme(palette, 15), parent: document.body });
    expect(view.state.facet(EditorView.darkTheme)).toBe(palette.isDark);
    const styles = css();
    expect(styles).toContain(`color: ${text}`);
    expect(styles).toContain(`color: ${keyword}`);
    expect(styles).toContain(`background-color: ${gitGutterColors(palette.isDark).added}`);
    expect(styles).toContain("font-size: 15px");
  });

  it("falls back to 13px text without a font size", () => {
    view = new EditorView({ doc: "x", extensions: buildEditorTheme(darkColors), parent: document.body });
    expect(css()).toContain("font-size: 13px");
  });
});
