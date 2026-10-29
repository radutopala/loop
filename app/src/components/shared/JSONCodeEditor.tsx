import "@fontsource/jetbrains-mono/400.css";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json } from "@codemirror/lang-json";
import { bracketMatching, foldGutter, foldKeymap } from "@codemirror/language";
import { search, searchKeymap } from "@codemirror/search";
import { Annotation, Compartment, EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from "@codemirror/view";
import { useEffect, useRef } from "react";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { buildEditorTheme } from "../panels/editorTheme";

// Marks the doc replacement that syncs value in, so it isn't reported back
// as an edit.
const externalValue = Annotation.define<boolean>();

/**
 * JSONCodeEditor edits value as JSON in CodeMirror, with fold arrows on
 * objects and arrays. onChange gets every edit; Mod-S calls onSave.
 */
export function JSONCodeEditor({ value, onChange, onSave, colors }: { value: string; onChange: (v: string) => void; onSave: () => void; colors: ColorPalette }) {
  const { fontSizes } = useTheme();
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const themeCompartment = useRef(new Compartment());
  // The editor is built once, so its handlers call the latest props.
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const onSaveRef = useRef(onSave);
  onSaveRef.current = onSave;

  // Built once; value, colors and the font size sync in below.
  useEffect(() => {
    if (!hostRef.current) return;
    const view = new EditorView({
      parent: hostRef.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLine(),
          highlightActiveLineGutter(),
          drawSelection(),
          bracketMatching(),
          foldGutter(),
          history(),
          search({ top: true }),
          json(),
          EditorView.theme({ "&": { height: "100%" }, ".cm-scroller": { overflow: "auto" } }),
          themeCompartment.current.of(buildEditorTheme(colors, fontSizes.panels)),
          keymap.of([
            {
              key: "Mod-s",
              preventDefault: true,
              run: () => {
                onSaveRef.current();
                return true;
              },
            },
            ...defaultKeymap,
            ...historyKeymap,
            ...foldKeymap,
            ...searchKeymap,
            indentWithTab,
          ]),
          EditorView.updateListener.of((update) => {
            if (update.docChanged && !update.transactions.some((tr) => tr.annotation(externalValue))) {
              onChangeRef.current(update.state.doc.toString());
            }
          }),
        ],
      }),
    });
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, []);

  // A value set from outside (a load, a save) replaces the doc.
  useEffect(() => {
    const view = viewRef.current;
    if (!view || view.state.doc.toString() === value) return;
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value }, annotations: externalValue.of(true) });
  }, [value]);

  useEffect(() => {
    viewRef.current?.dispatch({ effects: themeCompartment.current.reconfigure(buildEditorTheme(colors, fontSizes.panels)) });
  }, [colors, fontSizes.panels]);

  return <div ref={hostRef} data-testid="config-json-editor" style={{ height: "100%" }} />;
}
