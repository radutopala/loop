import { EditorView } from "@codemirror/view";
import { syntaxHighlighting, HighlightStyle } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { type ColorPalette, fonts } from "../../theme";
import { gitGutterColors } from "./editorGitGutter";

// Build CodeMirror editor theme + syntax highlighting from the active palette.
// Dark values match GoLand Darcula; light values match JetBrains IntelliJ Light.
export function buildEditorTheme(palette: ColorPalette, editorFontSize?: number) {
  const isDark = palette.isDark;

  // --- chrome / UI colors ---
  const textColor = isDark ? "#a9b7c6" : "#080808";
  const caretColor = isDark ? "#bbbbbb" : "#000000";
  const selectionBg = isDark ? "#214283" : "#a6d2ff";
  const gutterText = isDark ? "#606366" : "#999999";
  const activeGutterText = isDark ? "#a4a3a3" : "#444444";
  const activeLineBg = isDark ? "rgba(255,255,255,0.04)" : "rgba(0,0,0,0.03)";
  const matchBracketBg = isDark ? "#3b514d" : "#b4eeb4";
  const matchBracketText = isDark ? "#ffef28" : "#000000";
  const selectionMatchBg = isDark ? "rgba(33,66,131,0.4)" : "rgba(166,210,255,0.5)";
  const foldBg = isDark ? "#3c3f41" : "#e8e8e8";
  const tooltipBg = isDark ? "#3c3f41" : "#f7f7f7";
  const tooltipBorder = isDark ? "#555" : "#c0c0c0";
  const panelBtnColor = isDark ? "#ddd" : "#333";
  const panelBtnBorder = isDark ? "rgba(255,255,255,0.5)" : "rgba(0,0,0,0.3)";
  const panelBtnHoverBg = isDark ? "rgba(255,255,255,0.15)" : "rgba(0,0,0,0.08)";
  const panelBtnHoverBorderColor = isDark ? "#fff" : "#666";
  const panelBtnHoverColor = isDark ? "#fff" : "#000";
  const textfieldFocusBorder = isDark ? "rgba(255,255,255,0.5)" : "rgba(0,0,0,0.4)";
  const labelColor = isDark ? "#999" : "#666";
  const labelBorder = isDark ? "rgba(255,255,255,0.25)" : "rgba(0,0,0,0.2)";
  const labelHoverBorder = isDark ? "rgba(255,255,255,0.5)" : "rgba(0,0,0,0.4)";
  const labelHoverColor = isDark ? "#ccc" : "#333";

  // VCS gutter change bars (JetBrains/GoLand style): solid green for added,
  // blue for modified, grey triangle for deleted. Saturated so a 3px strip
  // reads clearly against the gutter background. Shared with the overview ruler.
  const { added: gitAddedBar, modified: gitModifiedBar, deleted: gitDeletedTri } = gitGutterColors(isDark);

  const theme = EditorView.theme({
    "&": {
      backgroundColor: palette.sidebar,
      color: textColor,
      fontSize: `${editorFontSize ?? 13}px`,
      fontFamily: "'JetBrains Mono', " + fonts.mono,
    },
    ".cm-content": {
      caretColor,
      padding: "4px 0",
    },
    ".cm-cursor, .cm-dropCursor": {
      borderLeftColor: caretColor,
    },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
      backgroundColor: selectionBg + " !important",
    },
    ".cm-gutters": {
      backgroundColor: palette.sidebar,
      color: gutterText,
      borderRight: `1px solid ${palette.border}`,
    },
    ".cm-activeLineGutter": {
      backgroundColor: palette.selectedBg,
      color: activeGutterText,
    },
    ".cm-activeLine": {
      backgroundColor: activeLineBg,
    },
    ".cm-matchingBracket": {
      backgroundColor: matchBracketBg,
      color: matchBracketText + " !important",
      outline: "none",
    },
    ".cm-selectionMatch": {
      backgroundColor: selectionMatchBg,
    },
    ".cm-foldPlaceholder": {
      backgroundColor: foldBg,
      color: textColor,
      border: "none",
    },
    ".cm-tooltip": {
      backgroundColor: tooltipBg,
      border: `1px solid ${tooltipBorder}`,
      color: textColor,
    },
    ".cm-panels": {
      backgroundColor: palette.surface,
      color: textColor,
      borderBottom: `1px solid ${palette.border}`,
      padding: "6px 8px",
      fontSize: "13px",
      gap: "4px",
    },
    ".cm-panels button": {
      backgroundImage: "none",
      backgroundColor: palette.hoverBg,
      color: panelBtnColor,
      border: `1px solid ${panelBtnBorder}`,
      borderRadius: "12px",
      cursor: "pointer",
      padding: "3px 10px",
      fontSize: "12px",
      lineHeight: "1.3",
    },
    ".cm-panels button:hover": {
      backgroundColor: panelBtnHoverBg,
      borderColor: panelBtnHoverBorderColor,
      color: panelBtnHoverColor,
    },
    ".cm-panels button[name=close]": {
      padding: "3px 6px",
    },
    ".cm-textfield": {
      backgroundColor: palette.bg,
      color: textColor,
      border: `1px solid ${palette.border}`,
      borderRadius: "4px",
      outline: "none",
      padding: "3px 6px",
      fontSize: "13px",
    },
    ".cm-textfield:focus": {
      borderColor: textfieldFocusBorder,
    },
    ".cm-panels label": {
      color: labelColor,
      fontSize: "11px",
      display: "inline-flex",
      alignItems: "center",
      cursor: "pointer",
      borderRadius: "12px",
      padding: "2px 8px",
      border: `1px solid ${labelBorder}`,
      gap: "0",
    },
    ".cm-panels label:hover": {
      borderColor: labelHoverBorder,
      color: labelHoverColor,
    },
    ".cm-panels label:has(input:checked)": {
      backgroundColor: palette.active,
      borderColor: palette.active,
      color: "#fff",
    },
    ".cm-panels input[type=checkbox]": {
      appearance: "none",
      width: "0",
      height: "0",
      margin: "0",
      padding: "0",
      border: "none",
      position: "absolute",
      opacity: "0",
    },
    ".cm-search": {
      gap: "4px",
    },
    // VCS change gutter: a thin strip at the far-left edge of the gutters.
    ".cm-gitChangeGutter": {
      width: "3px",
      padding: "0",
    },
    ".cm-gitChangeGutter .cm-gutterElement": {
      padding: "0",
    },
    ".cm-gitChange": {
      width: "3px",
      height: "100%",
      boxSizing: "border-box",
    },
    ".cm-gitChange-added": {
      backgroundColor: gitAddedBar,
    },
    ".cm-gitChange-modified": {
      backgroundColor: gitModifiedBar,
    },
    // Deletion marker: a small triangle hugging the top of the line below the
    // removed region, pointing into the document.
    ".cm-gitChange-deleted": {
      width: "0",
      height: "0",
      borderTop: "4px solid transparent",
      borderBottom: "4px solid transparent",
      borderLeft: `5px solid ${gitDeletedTri}`,
    },
  }, { dark: isDark });

  // --- syntax highlighting ---
  const kw = isDark ? "#cc7832" : "#0033b3";
  const num = isDark ? "#6897bb" : "#1750eb";
  const str = isDark ? "#6a8759" : "#067d17";
  const cmt = isDark ? "#808080" : "#8c8c8c";
  const docCmt = isDark ? "#629755" : "#8c8c8c";
  const fn = isDark ? "#ffc66d" : "#00627a";
  const prop = isDark ? "#9876aa" : "#871094";
  const varColor = isDark ? "#a9b7c6" : "#000000";
  const op = isDark ? "#a9b7c6" : "#000000";
  const tag_ = isDark ? "#e8bf6a" : "#0033b3";
  const attr = isDark ? "#bababa" : "#174ad4";
  const meta = isDark ? "#bbb529" : "#808000";
  const link = isDark ? "#287bde" : "#006dcc";
  const heading = isDark ? "#ffc66d" : "#0033b3";

  const highlight = syntaxHighlighting(HighlightStyle.define([
    { tag: tags.keyword, color: kw },
    { tag: tags.controlKeyword, color: kw },
    { tag: tags.operatorKeyword, color: kw },
    { tag: tags.definitionKeyword, color: kw },
    { tag: tags.moduleKeyword, color: kw },
    { tag: tags.operator, color: op },
    { tag: tags.separator, color: kw },
    { tag: tags.punctuation, color: op },
    { tag: tags.bracket, color: op },
    { tag: tags.number, color: num },
    { tag: tags.bool, color: kw },
    { tag: tags.null, color: kw },
    { tag: tags.self, color: kw },
    { tag: tags.atom, color: kw },
    { tag: tags.string, color: str },
    { tag: tags.special(tags.string), color: str },
    { tag: tags.regexp, color: str },
    { tag: tags.escape, color: kw },
    { tag: tags.comment, color: cmt, fontStyle: "italic" },
    { tag: tags.lineComment, color: cmt, fontStyle: "italic" },
    { tag: tags.blockComment, color: cmt, fontStyle: "italic" },
    { tag: tags.docComment, color: docCmt, fontStyle: "italic" },
    { tag: tags.variableName, color: varColor },
    { tag: tags.definition(tags.variableName), color: fn },
    { tag: tags.function(tags.variableName), color: fn },
    { tag: tags.typeName, color: fn },
    { tag: tags.className, color: fn },
    { tag: tags.definition(tags.typeName), color: fn },
    { tag: tags.definition(tags.propertyName), color: fn },
    { tag: tags.propertyName, color: prop },
    { tag: tags.special(tags.variableName), color: prop },
    { tag: tags.attributeName, color: attr },
    { tag: tags.attributeValue, color: str },
    { tag: tags.tagName, color: tag_ },
    { tag: tags.angleBracket, color: op },
    { tag: tags.meta, color: meta },
    { tag: tags.annotation, color: meta },
    { tag: tags.processingInstruction, color: meta },
    { tag: tags.link, color: link, textDecoration: "underline" },
    { tag: tags.heading, color: heading, fontWeight: "bold" },
    { tag: tags.emphasis, fontStyle: "italic" },
    { tag: tags.strong, fontWeight: "bold" },
    { tag: tags.strikethrough, textDecoration: "line-through" },
  ]));

  return [theme, highlight];
}
