import "@fontsource/jetbrains-mono/400.css";
import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from "@codemirror/view";
import { EditorSelection, EditorState, Compartment } from "@codemirror/state";
import { defaultKeymap, indentWithTab, history, historyKeymap } from "@codemirror/commands";
import { search, searchKeymap, openSearchPanel } from "@codemirror/search";
import { bracketMatching, foldGutter, foldKeymap } from "@codemirror/language";
import { javascript } from "@codemirror/lang-javascript";
import { go } from "@codemirror/lang-go";
import { python } from "@codemirror/lang-python";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { css } from "@codemirror/lang-css";
import { html } from "@codemirror/lang-html";
import { yaml } from "@codemirror/lang-yaml";
import { marked } from "marked";
import { fonts } from "../../theme";
import { isVideoPath } from "../../api/files";
import { useTheme } from "../../ThemeContext";
import { buildMarkdownStyles } from "./FilePanel";
import { buildEditorTheme } from "./editorTheme";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";

// ── Helpers ──

export function getLangExtension(filename: string) {
  const ext = filename.split(".").pop()?.toLowerCase();
  switch (ext) {
    case "js": case "jsx": case "mjs": case "cjs":
      return javascript();
    case "ts": case "tsx":
      return javascript({ typescript: true, jsx: ext.includes("x") });
    case "go":
      return go();
    case "py":
      return python();
    case "json": case "jsonl":
      return json();
    case "md": case "mdx":
      return markdown();
    case "css": case "scss":
      return css();
    case "html": case "htm": case "svg":
      return html();
    case "yaml": case "yml":
      return yaml();
    default:
      return null;
  }
}

export function isMarkdownFile(path: string): boolean {
  const ext = path.split(".").pop()?.toLowerCase();
  return ext === "md" || ext === "mdx";
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// ── Imperative handle for parent access to the CodeMirror view ──

export interface CodeEditorHandle {
  /** Get the current document text. */
  getContent(): string | null;
  /** Replace the entire document with new content. */
  replaceContent(content: string): void;
  /** Append a trailing newline if missing. */
  appendNewlineIfMissing(): void;
  /** Select all text. */
  selectAll(): void;
  /** Open the search panel. */
  openSearch(): void;
  /** Get the current selection range. */
  getSelection(): { from: number; to: number; text: string } | null;
  /** Replace the current selection. */
  replaceSelection(text: string): void;
  /** Move the cursor to the given 1-based line and scroll it into view. */
  scrollToLine(line: number): void;
}

// ── Props ──

interface CodeEditorProps {
  /** File content to display. null = no file loaded. */
  fileContent: string | null;
  /** Whether the file is binary (not editable). */
  isBinary: boolean;
  /** Binary file size for display. */
  binarySize: number;
  /** Currently selected file path (relative), used for language detection. null = no file. */
  selectedRelPath: string | null;
  /** Full path key of the selected file, used as a dependency for re-initialization. */
  selectedPath: string | null;
  /** Whether file is loading. */
  loading: boolean;
  /** Error message to display, if any. */
  error: string | null;
  /** Markdown preview mode. */
  previewMode: "editor" | "both" | "preview";
  /** Called when the document changes (for dirty tracking). */
  onDocChanged: () => void;
  /** Called when markdown preview should update. */
  onPreviewUpdate: (html: string) => void;
  /** Editor context menu state. */
  editorMenu: { x: number; y: number } | null;
  /** Close the editor context menu. */
  onEditorMenuClose: () => void;
  /** Open the editor context menu. */
  onEditorContextMenu: (e: React.MouseEvent) => void;
  /** Pre-rendered preview HTML for markdown. */
  previewHtml: string;
  /** When set, render the file as an image via this URL instead of the text editor. */
  imageURL?: string | null;
}

export const CodeEditor = forwardRef<CodeEditorHandle, CodeEditorProps>(function CodeEditor(
  { fileContent, isBinary, binarySize, selectedRelPath, selectedPath, loading, error, previewMode, onDocChanged, onPreviewUpdate, editorMenu, onEditorMenuClose, onEditorContextMenu, previewHtml, imageURL },
  ref,
) {
  const { colors, fontSizes } = useTheme();
  const editorRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const themeCompartment = useRef(new Compartment());
  const scrollSyncSource = useRef<"editor" | "preview" | null>(null);
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Keep stable refs so the CodeMirror update listener can use latest callbacks.
  const onDocChangedRef = useRef(onDocChanged);
  onDocChangedRef.current = onDocChanged;
  const onPreviewUpdateRef = useRef(onPreviewUpdate);
  onPreviewUpdateRef.current = onPreviewUpdate;
  const selectedRelPathRef = useRef(selectedRelPath);
  selectedRelPathRef.current = selectedRelPath;

  const isMd = selectedRelPath ? isMarkdownFile(selectedRelPath) : false;

  // Expose imperative methods to the parent.
  useImperativeHandle(ref, () => ({
    getContent() {
      return viewRef.current?.state.doc.toString() ?? null;
    },
    replaceContent(content: string) {
      const view = viewRef.current;
      if (!view) return;
      const current = view.state.doc.toString();
      if (content !== current) {
        view.dispatch({ changes: { from: 0, to: current.length, insert: content } });
      }
    },
    appendNewlineIfMissing() {
      const view = viewRef.current;
      if (!view) return;
      const doc = view.state.doc.toString();
      if (doc.length > 0 && !doc.endsWith("\n")) {
        view.dispatch({ changes: { from: view.state.doc.length, insert: "\n" } });
      }
    },
    selectAll() {
      const view = viewRef.current;
      if (view) view.dispatch({ selection: { anchor: 0, head: view.state.doc.length } });
    },
    openSearch() {
      const view = viewRef.current;
      if (view) openSearchPanel(view);
    },
    getSelection() {
      const view = viewRef.current;
      if (!view) return null;
      const { from, to } = view.state.selection.main;
      return { from, to, text: view.state.sliceDoc(from, to) };
    },
    replaceSelection(text: string) {
      const view = viewRef.current;
      if (!view) return;
      const { from, to } = view.state.selection.main;
      view.dispatch({ changes: { from, to, insert: text } });
    },
    scrollToLine(line: number) {
      const view = viewRef.current;
      if (!view) return;
      const total = view.state.doc.lines;
      const target = Math.max(1, Math.min(line, total));
      const lineInfo = view.state.doc.line(target);
      view.dispatch({
        selection: EditorSelection.cursor(lineInfo.from),
        effects: EditorView.scrollIntoView(lineInfo.from, { y: "center" }),
      });
      view.focus();
    },
  }));

  // Mount/update CodeMirror editor.
  useEffect(() => {
    if (!editorRef.current || fileContent === null || isBinary) {
      if (viewRef.current) {
        viewRef.current.destroy();
        viewRef.current = null;
      }
      return;
    }

    if (viewRef.current) {
      viewRef.current.destroy();
      viewRef.current = null;
    }

    const extensions = [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      drawSelection(),
      EditorView.lineWrapping,
      bracketMatching(),
      foldGutter(),
      history(),
      search({ top: true }),
      themeCompartment.current.of(buildEditorTheme(colors, fontSizes.editor)),
      keymap.of([
        ...defaultKeymap,
        ...historyKeymap,
        ...foldKeymap,
        ...searchKeymap,
        indentWithTab,
      ]),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          onDocChangedRef.current();
          const curRel = selectedRelPathRef.current;
          if (curRel && isMarkdownFile(curRel)) {
            if (previewTimerRef.current) clearTimeout(previewTimerRef.current);
            previewTimerRef.current = setTimeout(() => {
              previewTimerRef.current = null;
              onPreviewUpdateRef.current(marked.parse(update.state.doc.toString(), { async: false }) as string);
            }, 300);
          }
        }
      }),
    ];

    const lang = selectedRelPath ? getLangExtension(selectedRelPath) : null;
    if (lang) extensions.push(lang);

    const state = EditorState.create({
      doc: fileContent,
      extensions,
    });

    const view = new EditorView({
      state,
      parent: editorRef.current,
    });

    viewRef.current = view;

    // Set initial markdown preview.
    if (selectedRelPath && isMarkdownFile(selectedRelPath)) {
      onPreviewUpdateRef.current(marked.parse(fileContent, { async: false }) as string);
    } else {
      onPreviewUpdateRef.current("");
    }

    // Sync editor scroll -> preview.
    const scroller = editorRef.current;
    const onEditorScroll = () => {
      if (scrollSyncSource.current === "preview" || !scroller) return;
      scrollSyncSource.current = "editor";
      const el = previewRef.current;
      if (el) {
        const pct = scroller.scrollTop / Math.max(1, scroller.scrollHeight - scroller.clientHeight);
        el.scrollTop = pct * (el.scrollHeight - el.clientHeight);
      }
      requestAnimationFrame(() => { scrollSyncSource.current = null; });
    };
    scroller?.addEventListener("scroll", onEditorScroll);

    return () => {
      scroller?.removeEventListener("scroll", onEditorScroll);
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileContent, isBinary, selectedPath]);

  // Reconfigure CodeMirror theme when the palette changes.
  useEffect(() => {
    if (viewRef.current) {
      viewRef.current.dispatch({
        effects: themeCompartment.current.reconfigure(buildEditorTheme(colors, fontSizes.editor)),
      });
    }
  }, [colors, fontSizes.editor]);

  const getEditorMenuItems = (): MenuItem[] => {
    const view = viewRef.current;
    const items: MenuItem[] = [];
    const hasSelection = view ? view.state.selection.main.from !== view.state.selection.main.to : false;
    if (hasSelection) {
      items.push({ label: "Copy", onClick: () => { if (view) { const sel = view.state.sliceDoc(view.state.selection.main.from, view.state.selection.main.to); navigator.clipboard.writeText(sel); } } });
      items.push({ label: "Cut", onClick: () => { if (view) { const sel = view.state.sliceDoc(view.state.selection.main.from, view.state.selection.main.to); navigator.clipboard.writeText(sel); view.dispatch({ changes: { from: view.state.selection.main.from, to: view.state.selection.main.to, insert: "" } }); } } });
    }
    items.push({ label: "Select All", separator: hasSelection, onClick: () => { if (view) view.dispatch({ selection: { anchor: 0, head: view.state.doc.length } }); } });
    items.push({ label: "Find...", separator: true, onClick: () => { if (view) openSearchPanel(view); } });
    return items;
  };

  return (
    <>
      {loading && (
        <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {error && (
        <div style={{ padding: 16, color: colors.error, fontSize: 13 }}>{error}</div>
      )}
      {isBinary && !imageURL && (
        <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>
          Binary file ({formatSize(binarySize)})
        </div>
      )}
      {imageURL && (
        <div
          style={{
            flex: 1,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            overflow: "auto",
            padding: 16,
            backgroundColor: colors.sidebar,
          }}
        >
          {isVideoPath(selectedRelPath || "") ? (
            <video
              src={imageURL}
              controls
              style={{ maxWidth: "100%", maxHeight: "100%" }}
            />
          ) : (
            <img
              src={imageURL}
              alt=""
              style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }}
            />
          )}
        </div>
      )}
      {!selectedPath && !loading && (
        <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Select a file</div>
      )}
      <div style={{ flex: 1, display: fileContent !== null && !isBinary ? "flex" : "none", overflow: "hidden" }} onContextMenu={onEditorContextMenu}>
        <div
          ref={editorRef}
          style={{
            flex: 1,
            overflow: "auto",
            display: isMd && previewMode === "preview" ? "none" : undefined,
          }}
        />
        {isMd && previewMode !== "editor" && previewHtml && (
          <>
            <div style={{ width: 1, backgroundColor: colors.border, flexShrink: 0 }} />
            <div
              ref={previewRef}
              className="readme-content"
              dangerouslySetInnerHTML={{ __html: previewHtml }}
              onScroll={() => {
                if (scrollSyncSource.current === "editor") return;
                scrollSyncSource.current = "preview";
                const el = previewRef.current;
                const ed = editorRef.current;
                if (el && ed) {
                  const pct = el.scrollTop / Math.max(1, el.scrollHeight - el.clientHeight);
                  ed.scrollTop = pct * (ed.scrollHeight - ed.clientHeight);
                }
                requestAnimationFrame(() => { scrollSyncSource.current = null; });
              }}
              style={{
                flex: 1,
                overflow: "auto",
                padding: "12px 16px",
                fontSize: 13,
                fontFamily: fonts.sans,
                color: colors.text,
                lineHeight: 1.7,
                backgroundColor: colors.sidebar,
              }}
            />
            <style>{buildMarkdownStyles(colors)}</style>
          </>
        )}
      </div>
      {editorMenu && (
        <ContextMenu
          x={editorMenu.x}
          y={editorMenu.y}
          items={getEditorMenuItems()}
          onClose={onEditorMenuClose}
        />
      )}
    </>
  );
});
