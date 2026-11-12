import { indentUnit } from "@codemirror/language";
import { EditorState, type Extension } from "@codemirror/state";

/** How the editor indents: tab width, and spaces or a tab per indent. */
export function indentExtension(tabSize: number, insertSpaces: boolean): Extension {
  const size = Number.isInteger(tabSize) && tabSize >= 1 && tabSize <= 16 ? tabSize : 4;
  return [EditorState.tabSize.of(size), indentUnit.of(insertSpaces ? " ".repeat(size) : "\t")];
}
