import "@fontsource/jetbrains-mono/400.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from "@codemirror/view";
import { EditorState, Compartment } from "@codemirror/state";
import { defaultKeymap, indentWithTab, history, historyKeymap } from "@codemirror/commands";
import { search, searchKeymap } from "@codemirror/search";
import { bracketMatching, foldGutter, foldKeymap } from "@codemirror/language";
import { markdown } from "@codemirror/lang-markdown";
import { marked } from "marked";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import { fetchMemoryFiles, fetchMemoryFileContent, saveMemoryFileContent, type MemoryFileInfo } from "../api/loopApi";
import { FilePanel, buildMarkdownStyles } from "./FilePanel";
import { buildEditorTheme } from "./editorTheme";

const TREE_MIN_WIDTH = 120;
const TREE_MAX_WIDTH = 400;
const TREE_DEFAULT_WIDTH = 200;
const TREE_WIDTH_KEY = "loop-memory-tree-width";
const TABS_KEY = "loop-memory-tabs";

function loadTreeWidth(): number {
  try {
    const stored = localStorage.getItem(TREE_WIDTH_KEY);
    if (stored) {
      const w = parseInt(stored, 10);
      if (w >= TREE_MIN_WIDTH && w <= TREE_MAX_WIDTH) return w;
    }
  } catch { /* ignore */ }
  return TREE_DEFAULT_WIDTH;
}

interface TabsState { tabs: string[]; selected: string | null; }

function loadTabs(channelId: string): TabsState {
  try {
    const stored = localStorage.getItem(TABS_KEY);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        return all[channelId];
      }
    }
  } catch { /* ignore */ }
  return { tabs: [], selected: null };
}

function saveTabs(channelId: string, state: TabsState) {
  try {
    const stored = localStorage.getItem(TABS_KEY);
    const all = stored ? JSON.parse(stored) : {};
    if (state.tabs.length > 0) {
      all[channelId] = state;
    } else {
      delete all[channelId];
    }
    localStorage.setItem(TABS_KEY, JSON.stringify(all));
  } catch { /* ignore */ }
}

interface MemoryPanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  tabBar?: React.ReactNode;
  embedded?: boolean;
  openMemoryFile?: string | null;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function MemoryPanel({ channelId, dirPath, branch, embedded, openMemoryFile, ...panelProps }: MemoryPanelProps) {
  const { colors } = useTheme();
  const [files, setFiles] = useState<MemoryFileInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [treeWidth, setTreeWidth] = useState(loadTreeWidth);
  const [treeResizing, setTreeResizing] = useState(false);

  const [openTabs, setOpenTabs] = useState<string[]>(() => loadTabs(channelId).tabs);
  const [selectedPath, setSelectedPath] = useState<string | null>(() => loadTabs(channelId).selected);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [contentError, setContentError] = useState<string | null>(null);
  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [previewVisible, setPreviewVisible] = useState(false);
  const [previewHtml, setPreviewHtml] = useState("");
  const [autoSaveOnBlur, setAutoSaveOnBlur] = useState(true);

  const editorRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const themeCompartment = useRef(new Compartment());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;
  const dirtyTabsRef = useRef(dirtyTabs);
  dirtyTabsRef.current = dirtyTabs;
  const autoSaveOnBlurRef = useRef(autoSaveOnBlur);
  autoSaveOnBlurRef.current = autoSaveOnBlur;
  const dirtyContentRef = useRef(new Map<string, string>());

  // Load settings.
  useEffect(() => {
    window.loopAPI?.getSettings?.().then((s) => {
      if (typeof s.autoSaveOnBlur === "boolean") setAutoSaveOnBlur(s.autoSaveOnBlur);
    }).catch(() => {});
  }, []);

  // Persist tabs.
  useEffect(() => {
    saveTabs(channelId, { tabs: openTabs, selected: selectedPath });
  }, [channelId, openTabs, selectedPath]);

  // Fetch file list.
  useEffect(() => {
    setLoading(true);
    setListError(null);
    setFiles([]);
    fetchMemoryFiles(channelId)
      .then((f) => {
        setFiles(f);
        // If we have open tabs, keep them. Otherwise select the first file.
        if (openTabs.length === 0 && f.length > 0 && f[0]) {
          const firstPath = f[0].file_path;
          setOpenTabs([firstPath]);
          setSelectedPath(firstPath);
        }
      })
      .catch((err) => setListError(err instanceof Error ? err.message : "Failed to load"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  // Handle openMemoryFile prop (from Cmd+K navigation).
  useEffect(() => {
    if (!openMemoryFile) return;
    openFileInTab(openMemoryFile);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openMemoryFile]);

  const markDirty = useCallback(() => {
    const p = selectedPathRef.current;
    if (p) {
      setDirtyTabs((prev) => { if (prev.has(p)) return prev; const next = new Set(prev); next.add(p); return next; });
    }
  }, []);

  const saveFile = useCallback((filePath?: string) => {
    const savePath = filePath ?? selectedPathRef.current;
    if (!savePath) return;
    const view = viewRef.current;
    if (!view || savePath !== selectedPathRef.current) return;
    let content = view.state.doc.toString();
    if (content.length > 0 && !content.endsWith("\n")) {
      content += "\n";
      view.dispatch({ changes: { from: view.state.doc.length, insert: "\n" } });
    }
    saveMemoryFileContent(savePath, content).then(() => {
      dirtyContentRef.current.delete(savePath);
      setDirtyTabs((prev) => { if (!prev.has(savePath)) return prev; const next = new Set(prev); next.delete(savePath); return next; });
    }).catch(() => {});
  }, []);

  const saveAllDirty = useCallback(() => {
    if (dirtyTabsRef.current.has(selectedPathRef.current ?? "")) {
      saveFile();
    }
  }, [saveFile]);

  // Save on unmount if auto-save enabled.
  useEffect(() => {
    return () => { if (autoSaveOnBlurRef.current) saveAllDirty(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  const updatePreview = useCallback((doc: string) => {
    if (previewTimerRef.current) clearTimeout(previewTimerRef.current);
    previewTimerRef.current = setTimeout(() => {
      previewTimerRef.current = null;
      setPreviewHtml(marked.parse(doc, { async: false }) as string);
    }, 300);
  }, []);

  const switchToTab = useCallback((path: string) => {
    const curPath = selectedPathRef.current;
    if (curPath && dirtyTabsRef.current.has(curPath)) {
      const view = viewRef.current;
      if (view) dirtyContentRef.current.set(curPath, view.state.doc.toString());
      if (autoSaveOnBlurRef.current) saveAllDirty();
    }
    setSelectedPath(path);
    setContentError(null);
    const cached = dirtyContentRef.current.get(path);
    if (cached !== undefined) {
      setFileContent(cached);
    } else {
      setFileContent(null);
      fetchMemoryFileContent(path).then((content) => {
        setFileContent(content);
      }).catch((err) => {
        setContentError(err instanceof Error ? err.message : "Failed to load file");
      });
    }
  }, [saveAllDirty]);

  const openFileInTab = useCallback((path: string) => {
    setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
    if (selectedPathRef.current !== path) switchToTab(path);
  }, [switchToTab]);

  const handleFileClick = useCallback((path: string) => {
    openFileInTab(path);
  }, [openFileInTab]);

  const handleCloseTab = useCallback((path: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (autoSaveOnBlurRef.current && path === selectedPath) saveAllDirty();
    dirtyContentRef.current.delete(path);
    setDirtyTabs((prev) => { if (!prev.has(path)) return prev; const next = new Set(prev); next.delete(path); return next; });
    setOpenTabs((prev) => {
      const next = prev.filter((p) => p !== path);
      if (path === selectedPath) {
        if (next.length > 0) {
          const idx = Math.min(prev.indexOf(path), next.length - 1);
          switchToTab(next[Math.max(0, idx)]!);
        } else {
          setSelectedPath(null);
          setFileContent(null);
          setContentError(null);
        }
      }
      return next;
    });
  }, [selectedPath, saveAllDirty, switchToTab]);

  // Load selected file content on mount.
  useEffect(() => {
    if (!selectedPath) return;
    fetchMemoryFileContent(selectedPath).then((content) => {
      setFileContent(content);
    }).catch((err) => {
      setContentError(err instanceof Error ? err.message : "Failed to load file");
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      themeCompartment.current.of(buildEditorTheme(colors)),
      keymap.of([
        ...defaultKeymap,
        ...historyKeymap,
        ...foldKeymap,
        ...searchKeymap,
        indentWithTab,
      ]),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          markDirty();
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

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileContent, selectedPath]);

  // Reconfigure theme when palette changes.
  useEffect(() => {
    if (viewRef.current) {
      viewRef.current.dispatch({
        effects: themeCompartment.current.reconfigure(buildEditorTheme(colors)),
      });
    }
  }, [colors]);

  // Cmd+S keyboard shortcut.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        saveAllDirty();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [saveAllDirty]);

  // Save on blur, reload on focus.
  useEffect(() => {
    const onBlur = () => { if (autoSaveOnBlur) saveAllDirty(); };
    const onFocus = () => {
      const path = selectedPathRef.current;
      if (!path) return;
      fetchMemoryFileContent(path).then((content) => {
        if (selectedPathRef.current !== path) return;
        const view = viewRef.current;
        if (!view) return;
        const current = view.state.doc.toString();
        if (content !== current) {
          view.dispatch({ changes: { from: 0, to: current.length, insert: content } });
          setDirtyTabs((prev) => { if (!prev.has(path)) return prev; const next = new Set(prev); next.delete(path); return next; });
          setPreviewHtml(marked.parse(content, { async: false }) as string);
        }
      }).catch(() => {});
    };
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);
    return () => { window.removeEventListener("blur", onBlur); window.removeEventListener("focus", onFocus); };
  }, [saveAllDirty, autoSaveOnBlur]);

  const handleTreeResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setTreeResizing(true);
    const startX = e.clientX;
    const startWidth = treeWidth;
    let lastWidth = startWidth;

    const onMouseMove = (ev: MouseEvent) => {
      const newWidth = Math.min(TREE_MAX_WIDTH, Math.max(TREE_MIN_WIDTH, startWidth + (ev.clientX - startX)));
      lastWidth = newWidth;
      setTreeWidth(newWidth);
    };

    const onMouseUp = () => {
      setTreeResizing(false);
      try { localStorage.setItem(TREE_WIDTH_KEY, String(lastWidth)); } catch { /* ignore */ }
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, [treeWidth]);

  // Group files by dir_path.
  const groups = useMemo(() => {
    const map = new Map<string, MemoryFileInfo[]>();
    for (const f of files) {
      const list = map.get(f.dir_path) || [];
      list.push(f);
      map.set(f.dir_path, list);
    }
    return map;
  }, [files]);

  const multipleGroups = groups.size > 1;

  const fileName = (path: string) => path.split("/").pop() || path;

  return (
    <FilePanel title="Memory" dirPath={dirPath} branch={branch} noPadding={!loading && files.length > 0} embedded={embedded} {...panelProps}>
      {listError && (
        <div style={{ color: colors.error, fontSize: 13 }}>{listError}</div>
      )}
      {loading && (
        <div style={{ color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {!loading && !listError && files.length === 0 && (
        <div style={{ color: colors.textDim, fontSize: 13 }}>No memory files indexed</div>
      )}
      {!loading && files.length > 0 && (
        <div style={{ display: "flex", height: "100%", flexDirection: "column" }}>
          {/* Tab bar */}
          {openTabs.length > 0 && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                borderBottom: `1px solid ${colors.border}`,
                height: 30,
                flexShrink: 0,
                overflow: "auto",
                gap: 0,
              }}
            >
              {openTabs.map((path) => {
                const isActive = path === selectedPath;
                const isDirty = dirtyTabs.has(path);
                return (
                  <div
                    key={path}
                    onClick={() => { if (!isActive) switchToTab(path); }}
                    title={path}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 4,
                      padding: "0 8px",
                      height: "100%",
                      fontSize: 11,
                      fontFamily: fonts.mono,
                      cursor: "pointer",
                      color: isActive ? colors.textLight : colors.textDim,
                      backgroundColor: isActive ? colors.sidebar : "transparent",
                      borderRight: `1px solid ${colors.border}`,
                      whiteSpace: "nowrap",
                    }}
                  >
                    <span>{isDirty ? "\u25CF " : ""}{fileName(path)}</span>
                    <button
                      onClick={(e) => handleCloseTab(path, e)}
                      style={{
                        background: "none",
                        border: "none",
                        color: "inherit",
                        cursor: "pointer",
                        padding: 0,
                        lineHeight: 1,
                        fontSize: 12,
                        opacity: 0.6,
                      }}
                    >
                      &times;
                    </button>
                  </div>
                );
              })}
              <div style={{ flex: 1 }} />
              {/* Preview toggle */}
              <button
                onClick={() => setPreviewVisible((v) => !v)}
                title={previewVisible ? "Hide preview" : "Show preview"}
                style={{
                  background: previewVisible ? colors.selectedBg : "none",
                  border: "none",
                  color: previewVisible ? colors.textLight : colors.textDim,
                  cursor: "pointer",
                  padding: "2px 8px",
                  fontSize: 10,
                  fontFamily: fonts.mono,
                  flexShrink: 0,
                }}
              >
                Preview
              </button>
            </div>
          )}
          <div style={{ display: "flex", flex: 1, overflow: "hidden", userSelect: treeResizing ? "none" : undefined }}>
            {/* File tree */}
            <div
              style={{
                width: treeWidth,
                minWidth: TREE_MIN_WIDTH,
                maxWidth: TREE_MAX_WIDTH,
                overflow: "auto",
                padding: "8px 0",
                flexShrink: 0,
              }}
            >
              {[...groups.entries()].map(([dp, groupFiles]) => (
                <div key={dp}>
                  {multipleGroups && (
                    <div
                      style={{
                        fontSize: 10,
                        fontWeight: 700,
                        color: colors.textDim,
                        textTransform: "uppercase",
                        letterSpacing: 0.5,
                        padding: "6px 10px 2px",
                        whiteSpace: "nowrap",
                      }}
                      title={dp}
                    >
                      {dp.split("/").pop() || dp}
                    </div>
                  )}
                  {groupFiles.map((f) => {
                    const name = fileName(f.file_path);
                    const isSelected = f.file_path === selectedPath;
                    const isOpen = openTabs.includes(f.file_path);
                    return (
                      <button
                        key={f.file_path}
                        onClick={() => handleFileClick(f.file_path)}
                        title={f.file_path}
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 6,
                          width: "max-content",
                          minWidth: "100%",
                          padding: "4px 10px",
                          border: "none",
                          background: isSelected ? colors.selectedBg : "none",
                          color: isSelected ? colors.textLight : colors.text,
                          cursor: "pointer",
                          fontSize: 12,
                          fontFamily: fonts.mono,
                          fontWeight: isOpen ? 600 : 400,
                          textAlign: "left",
                          whiteSpace: "nowrap",
                        }}
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                          <polyline points="14 2 14 8 20 8" />
                        </svg>
                        {name}
                      </button>
                    );
                  })}
                </div>
              ))}
            </div>
            {/* Tree resize handle */}
            <div
              onMouseDown={handleTreeResize}
              style={{
                width: 4,
                cursor: "col-resize",
                backgroundColor: treeResizing ? colors.textDim : "transparent",
                flexShrink: 0,
                borderRight: `1px solid ${colors.border}`,
              }}
              onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim; }}
              onMouseLeave={(e) => { if (!treeResizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent"; }}
            />
            {/* Editor area */}
            <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
              {!selectedPath && (
                <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: 13 }}>
                  Select a file
                </div>
              )}
              {selectedPath && contentError && (
                <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: 13, fontStyle: "italic" }}>
                  File not available on disk
                </div>
              )}
              {selectedPath && !contentError && fileContent === null && (
                <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: 13 }}>
                  Loading...
                </div>
              )}
              {selectedPath && fileContent !== null && (
                <>
                  <div
                    ref={editorRef}
                    style={{
                      flex: 1,
                      overflow: "auto",
                      display: "flex",
                      flexDirection: "column",
                    }}
                  />
                  {previewVisible && (
                    <div
                      style={{
                        flex: 1,
                        overflowY: "auto",
                        padding: "12px 16px",
                        borderLeft: `1px solid ${colors.border}`,
                      }}
                    >
                      <div
                        className="readme-content"
                        dangerouslySetInnerHTML={{ __html: previewHtml }}
                        style={{
                          fontSize: 13,
                          fontFamily: fonts.sans,
                          color: colors.text,
                          lineHeight: 1.7,
                        }}
                      />
                      <style>{buildMarkdownStyles(colors)}</style>
                    </div>
                  )}
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </FilePanel>
  );
}
