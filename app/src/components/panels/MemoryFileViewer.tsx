import "@fontsource/jetbrains-mono/400.css";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { bracketMatching, foldGutter, foldKeymap } from "@codemirror/language";
import { search, searchKeymap } from "@codemirror/search";
import { Compartment, EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from "@codemirror/view";
import { marked } from "marked";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { buildEditorTheme } from "./editorTheme";
import { buildMarkdownStyles } from "./FilePanel";
import { MemoryFileIcon } from "./MemoryFileList";

export interface MemoryFileViewerProps {
  selectedPath: string | null;
  fileContent: string | null;
  contentError: string | null;
  openTabs: string[];
  dirtyTabs: Set<string>;
  previewTab: string | null;
  /** Shared EditorView ref — parent reads content from this for save/tab-switch. */
  viewRef: React.MutableRefObject<EditorView | null>;
  onSwitchToTab: (path: string) => void;
  onCloseTab: (path: string, e?: React.MouseEvent) => void;
  onSetPreviewTab: (path: string | null) => void;
  onMarkDirty: () => void;
  onSaveAllDirty: () => void;
}

export function MemoryFileViewer({
  selectedPath,
  fileContent,
  contentError,
  openTabs,
  dirtyTabs,
  previewTab,
  viewRef,
  onSwitchToTab,
  onCloseTab,
  onSetPreviewTab,
  onMarkDirty,
  onSaveAllDirty,
}: MemoryFileViewerProps) {
  const { colors, fontSizes } = useTheme();
  const [previewMode, setPreviewMode] = useState<"editor" | "both" | "preview">("editor");
  const [previewHtml, setPreviewHtml] = useState("");

  const editorRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const themeCompartment = useRef(new Compartment());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scrollSyncSource = useRef<"editor" | "preview" | null>(null);

  const updatePreview = useCallback((doc: string) => {
    if (previewTimerRef.current) clearTimeout(previewTimerRef.current);
    previewTimerRef.current = setTimeout(() => {
      previewTimerRef.current = null;
      setPreviewHtml(marked.parse(doc, { async: false }) as string);
    }, 300);
  }, []);

  // Mount/update CodeMirror editor.
  useEffect(() => {
    if (!editorRef.current || fileContent === null) {
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
      markdown(),
      themeCompartment.current.of(buildEditorTheme(colors, fontSizes.panels)),
      keymap.of([...defaultKeymap, ...historyKeymap, ...foldKeymap, ...searchKeymap, indentWithTab]),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          onMarkDirty();
          updatePreview(update.state.doc.toString());
        }
      }),
    ];

    const state = EditorState.create({
      doc: fileContent,
      extensions,
    });

    const view = new EditorView({
      state,
      parent: editorRef.current,
    });

    viewRef.current = view;
    setPreviewHtml(marked.parse(fileContent, { async: false }) as string);

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
      requestAnimationFrame(() => {
        scrollSyncSource.current = null;
      });
    };
    scroller?.addEventListener("scroll", onEditorScroll);

    return () => {
      scroller?.removeEventListener("scroll", onEditorScroll);
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileContent, selectedPath]);

  // Reconfigure theme when palette changes.
  useEffect(() => {
    if (viewRef.current) {
      viewRef.current.dispatch({
        effects: themeCompartment.current.reconfigure(buildEditorTheme(colors, fontSizes.panels)),
      });
    }
  }, [colors, fontSizes.panels, viewRef]);

  // Cmd+S keyboard shortcut.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        onSaveAllDirty();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onSaveAllDirty]);

  const fileName = (path: string) => path.split("/").pop() || path;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
      {/* Tab bar */}
      {openTabs.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            borderBottom: `1px solid ${colors.border}`,
            flexShrink: 0,
            overflow: "auto",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", flex: 1, minWidth: 0 }}>
            {openTabs.map((path) => {
              const isActive = path === selectedPath;
              const isDirty = dirtyTabs.has(path);
              const isPreview = path === previewTab;
              const name = fileName(path);
              return (
                <button
                  key={path}
                  onClick={() => {
                    if (!isActive) onSwitchToTab(path);
                  }}
                  onDoubleClick={() => {
                    if (isPreview) onSetPreviewTab(null);
                  }}
                  title={path}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 4,
                    padding: "5px 8px",
                    border: "none",
                    borderRight: `1px solid ${colors.border}`,
                    borderBottom: isActive ? `2px solid ${colors.active}` : "2px solid transparent",
                    background: isActive ? colors.sidebar : "transparent",
                    color: isActive ? colors.textLight : colors.textDim,
                    cursor: "pointer",
                    fontSize: 11,
                    fontFamily: fonts.mono,
                    whiteSpace: "nowrap",
                    flexShrink: 0,
                  }}
                  onMouseEnter={(e) => {
                    if (!isActive) e.currentTarget.style.backgroundColor = colors.hoverBg;
                  }}
                  onMouseLeave={(e) => {
                    if (!isActive) e.currentTarget.style.backgroundColor = "transparent";
                  }}
                >
                  <MemoryFileIcon />
                  <span style={{ fontStyle: isPreview || isDirty ? "italic" : undefined }}>{name}</span>
                  <span onClick={(e) => onCloseTab(path, e)} style={{ marginLeft: 2, width: 8, height: 8, display: "flex", alignItems: "center", justifyContent: "center" }}>
                    {isDirty ? (
                      <span style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.warning, display: "block" }} />
                    ) : (
                      <span
                        style={{ opacity: 0.5, fontSize: 14, lineHeight: 1 }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.opacity = "1";
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.opacity = "0.5";
                        }}
                      >
                        &times;
                      </span>
                    )}
                  </span>
                </button>
              );
            })}
          </div>
          {/* Preview mode pill */}
          <div style={{ display: "flex", flexShrink: 0, margin: "0 6px", border: `1px solid ${colors.border}`, borderRadius: 4, overflow: "hidden" }}>
            {(["editor", "both", "preview"] as const).map((mode) => (
              <button
                key={mode}
                onClick={() => setPreviewMode(mode)}
                title={mode === "editor" ? "Editor only" : mode === "both" ? "Editor + Preview" : "Preview only"}
                style={{
                  fontSize: 10,
                  color: previewMode === mode ? colors.active : colors.textDim,
                  background: previewMode === mode ? `${colors.active}18` : "none",
                  border: "none",
                  borderRight: mode !== "preview" ? `1px solid ${colors.border}` : undefined,
                  cursor: "pointer",
                  padding: "2px 6px",
                  lineHeight: 1,
                }}
              >
                {mode === "editor" ? "Edit" : mode === "both" ? "Split" : "Preview"}
              </button>
            ))}
          </div>
        </div>
      )}
      {/* Editor content */}
      {!selectedPath && <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Select a file</div>}
      {selectedPath && contentError && <div style={{ padding: 16, color: colors.textDim, fontSize: 13, fontStyle: "italic" }}>File not available on disk</div>}
      {selectedPath && !contentError && fileContent === null && <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Loading...</div>}
      <div style={{ flex: 1, display: fileContent !== null ? "flex" : "none", overflow: "hidden" }}>
        <div
          ref={editorRef}
          style={{
            flex: 1,
            overflow: "auto",
            display: previewMode === "preview" ? "none" : undefined,
          }}
        />
        {previewMode !== "editor" && previewHtml && (
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
                requestAnimationFrame(() => {
                  scrollSyncSource.current = null;
                });
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
    </div>
  );
}
