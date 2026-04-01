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
import { fetchGlobalConfig } from "../api/configApi";
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

// ── File icon (matches EditorPanel style for .md) ──

function MemoryFileIcon() {
  const color = "#519aba"; // .md color from EditorPanel
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" style={{ flexShrink: 0 }}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <polyline points="14 2 14 8 20 8" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <text x="12" y="18" textAnchor="middle" fill={color} fontSize="7" fontWeight="bold" fontFamily={fonts.mono}>M</text>
    </svg>
  );
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
  const { colors, fontSizes } = useTheme();
  const [files, setFiles] = useState<MemoryFileInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [treeWidth, setTreeWidth] = useState(loadTreeWidth);
  const [treeResizing, setTreeResizing] = useState(false);
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());

  const [openTabs, setOpenTabs] = useState<string[]>(() => loadTabs(channelId).tabs);
  const [selectedPath, setSelectedPath] = useState<string | null>(() => loadTabs(channelId).selected);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [contentError, setContentError] = useState<string | null>(null);
  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [previewTab, setPreviewTab] = useState<string | null>(null);
  const [previewMode, setPreviewMode] = useState<"editor" | "both" | "preview">("editor");
  const [previewHtml, setPreviewHtml] = useState("");
  const [autoSaveOnBlur, setAutoSaveOnBlur] = useState(false);
  const [previewTabsEnabled, setPreviewTabsEnabled] = useState(true);

  const editorRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const themeCompartment = useRef(new Compartment());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scrollSyncSource = useRef<"editor" | "preview" | null>(null);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;
  const dirtyTabsRef = useRef(dirtyTabs);
  dirtyTabsRef.current = dirtyTabs;
  const autoSaveOnBlurRef = useRef(autoSaveOnBlur);
  autoSaveOnBlurRef.current = autoSaveOnBlur;
  const channelIdRef = useRef(channelId);
  channelIdRef.current = channelId;
  const dirtyContentRef = useRef(new Map<string, string>());

  // Load desktop settings from global config.
  useEffect(() => {
    fetchGlobalConfig().then((cfg) => {
      const d = cfg.content?.desktop;
      if (!d) return;
      if (typeof d.auto_save_on_blur === "boolean") setAutoSaveOnBlur(d.auto_save_on_blur);
      if (typeof d.preview_tabs === "boolean") setPreviewTabsEnabled(d.preview_tabs);
    }).catch(() => {});
  }, []);

  // Persist tabs (exclude preview tab).
  useEffect(() => {
    const persistedTabs = previewTab ? openTabs.filter((t) => t !== previewTab) : openTabs;
    const persistedSelected = selectedPath === previewTab ? null : selectedPath;
    saveTabs(channelId, { tabs: persistedTabs, selected: persistedSelected });
  }, [channelId, openTabs, selectedPath, previewTab]);

  // Fetch file list.
  const loadFiles = useCallback(() => {
    setLoading(true);
    setListError(null);
    fetchMemoryFiles(channelId)
      .then((f) => {
        setFiles(f);
        // Auto-expand all dirs on first load.
        const dirs = new Set(f.map((fi) => fi.dir_path));
        setExpandedDirs(dirs);
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

  useEffect(() => {
    setFiles([]);
    loadFiles();
  }, [loadFiles]);

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
      // Editing promotes a preview tab to permanent.
      setPreviewTab((cur) => cur === p ? null : cur);
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
    saveMemoryFileContent(channelIdRef.current, savePath, content).then(() => {
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
      fetchMemoryFileContent(channelIdRef.current, path).then((content) => {
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

  // Single-click: open as preview (transient) tab, or permanently if preview disabled.
  const handleFileClick = useCallback((path: string) => {
    if (!previewTabsEnabled) {
      openFileInTab(path);
      return;
    }
    // If already a permanent tab, just switch.
    if (openTabs.includes(path) && path !== previewTab) {
      if (selectedPathRef.current !== path) switchToTab(path);
      return;
    }
    // Replace the existing preview tab with this file.
    setOpenTabs((prev) => {
      const without = previewTab ? prev.filter((t) => t !== previewTab) : prev;
      return without.includes(path) ? without : [...without, path];
    });
    setPreviewTab(path);
    if (selectedPathRef.current !== path) switchToTab(path);
  }, [previewTabsEnabled, openTabs, previewTab, switchToTab, openFileInTab]);

  // Double-click: promote preview to permanent.
  const handleFileDoubleClick = useCallback((path: string) => {
    setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
    if (previewTab === path) setPreviewTab(null);
    if (selectedPathRef.current !== path) switchToTab(path);
  }, [previewTab, switchToTab]);

  const handleCloseTab = useCallback((path: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (autoSaveOnBlurRef.current && path === selectedPath) saveAllDirty();
    dirtyContentRef.current.delete(path);
    setDirtyTabs((prev) => { if (!prev.has(path)) return prev; const next = new Set(prev); next.delete(path); return next; });
    if (previewTab === path) setPreviewTab(null);
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
    fetchMemoryFileContent(channelIdRef.current, selectedPath).then((content) => {
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
      themeCompartment.current.of(buildEditorTheme(colors, fontSizes.panels)),
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

    // Sync editor scroll → preview.
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
  }, [fileContent, selectedPath]);

  // Reconfigure theme when palette changes.
  useEffect(() => {
    if (viewRef.current) {
      viewRef.current.dispatch({
        effects: themeCompartment.current.reconfigure(buildEditorTheme(colors, fontSizes.panels)),
      });
    }
  }, [colors, fontSizes.panels]);

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
      fetchMemoryFileContent(channelIdRef.current, path).then((content) => {
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

  // Build a unified tree from all file paths, collapsing common prefixes.
  const tree = useMemo((): TreeNode[] => {
    const root: TreeNode = { name: "", children: [], isDir: true, key: "root" };

    for (const f of files) {
      const parts = f.file_path.split("/").filter(Boolean);
      let node = root;
      for (let i = 0; i < parts.length; i++) {
        const part = parts[i]!;
        const isFile = i === parts.length - 1;
        let child = node.children.find((c) => c.name === part && c.isDir === !isFile);
        if (!child) {
          const childKey = isFile ? `file:${f.file_path}` : `dir:/${parts.slice(0, i + 1).join("/")}`;
          child = { name: part, children: [], isDir: !isFile, fullPath: isFile ? f.file_path : undefined, key: childKey };
          node.children.push(child);
        }
        node = child;
      }
    }

    const sortChildren = (n: TreeNode) => {
      n.children.sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
      for (const c of n.children) if (c.isDir) sortChildren(c);
    };
    sortChildren(root);

    return root.children;
  }, [files]);

  // Auto-expand all dirs on first load.
  useEffect(() => {
    const keys = new Set<string>();
    const collect = (nodes: TreeNode[]) => {
      for (const n of nodes) {
        if (n.isDir) { keys.add(n.key); collect(n.children); }
      }
    };
    collect(tree);
    if (keys.size > 0) setExpandedDirs(keys);
  }, [tree]);

  const handleDirToggle = useCallback((key: string) => {
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }, []);

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
        <div style={{ display: "flex", height: "100%", userSelect: treeResizing ? "none" : undefined }}>
          {/* File tree — matches EditorPanel style */}
          <div
            style={{
              width: treeWidth,
              minWidth: TREE_MIN_WIDTH,
              maxWidth: TREE_MAX_WIDTH,
              overflow: "auto",
              flexShrink: 0,
              display: "flex",
              flexDirection: "column",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", padding: "4px 8px 2px", flexShrink: 0 }}>
              <span style={{ flex: 1, fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Files</span>
              <button
                onClick={loadFiles}
                title="Refresh files"
                style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center" }}
                onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="23 4 23 10 17 10" />
                  <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
                </svg>
              </button>
            </div>
            <div style={{ flex: 1, overflow: "auto", padding: "2px 0" }}>
              {tree.map((root) => (
                <MemoryTreeNode
                  key={root.key}
                  node={root}
                  depth={0}
                  expandedDirs={expandedDirs}
                  selectedPath={selectedPath}
                  onDirToggle={handleDirToggle}
                  onFileClick={handleFileClick}
                  onFileDoubleClick={handleFileDoubleClick}
                />
              ))}
            </div>
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
          {/* Editor area (tabs + content) */}
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
                        onClick={() => { if (!isActive) switchToTab(path); }}
                        onDoubleClick={() => { if (isPreview) setPreviewTab(null); }}
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
                        onMouseEnter={(e) => { if (!isActive) e.currentTarget.style.backgroundColor = colors.hoverBg; }}
                        onMouseLeave={(e) => { if (!isActive) e.currentTarget.style.backgroundColor = "transparent"; }}
                      >
                        <MemoryFileIcon />
                        <span style={{ fontStyle: isPreview || isDirty ? "italic" : undefined }}>{name}</span>
                        <span
                          onClick={(e) => handleCloseTab(path, e)}
                          style={{ marginLeft: 2, width: 8, height: 8, display: "flex", alignItems: "center", justifyContent: "center" }}
                        >
                          {isDirty ? (
                            <span style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.warning, display: "block" }} />
                          ) : (
                            <span
                              style={{ opacity: 0.5, fontSize: 14, lineHeight: 1 }}
                              onMouseEnter={(e) => { e.currentTarget.style.opacity = "1"; }}
                              onMouseLeave={(e) => { e.currentTarget.style.opacity = "0.5"; }}
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
            {!selectedPath && (
              <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Select a file</div>
            )}
            {selectedPath && contentError && (
              <div style={{ padding: 16, color: colors.textDim, fontSize: 13, fontStyle: "italic" }}>File not available on disk</div>
            )}
            {selectedPath && !contentError && fileContent === null && (
              <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Loading...</div>
            )}
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
          </div>
        </div>
      )}
    </FilePanel>
  );
}

// ── Recursive tree node ──

interface TreeNode {
  name: string;
  fullPath?: string;
  children: TreeNode[];
  isDir: boolean;
  key: string;
}

function MemoryTreeNode({ node, depth, expandedDirs, selectedPath, onDirToggle, onFileClick, onFileDoubleClick }: {
  node: TreeNode;
  depth: number;
  expandedDirs: Set<string>;
  selectedPath: string | null;
  onDirToggle: (key: string) => void;
  onFileClick: (path: string) => void;
  onFileDoubleClick: (path: string) => void;
}) {
  const { colors } = useTheme();
  const isExpanded = expandedDirs.has(node.key);

  if (node.isDir) {
    return (
      <div>
        <button
          onClick={() => onDirToggle(node.key)}
          title={node.name}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            width: "max-content",
            minWidth: "100%",
            padding: `3px 8px 3px ${8 + depth * 16}px`,
            border: "none",
            background: "none",
            color: colors.textLight,
            cursor: "pointer",
            fontSize: 12,
            fontFamily: fonts.mono,
            fontWeight: depth === 0 ? 700 : 400,
            textAlign: "left",
            whiteSpace: "nowrap",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ flexShrink: 0, opacity: 0.6, transform: isExpanded ? "rotate(90deg)" : "none", transition: "transform 0.1s" }}>
            <polyline points="3,1 7,5 3,9" />
          </svg>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
            <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
          </svg>
          {node.name}
        </button>
        {isExpanded && node.children.map((child) => (
          <MemoryTreeNode
            key={child.key}
            node={child}
            depth={depth + 1}
            expandedDirs={expandedDirs}
            selectedPath={selectedPath}
            onDirToggle={onDirToggle}
            onFileClick={onFileClick}
            onFileDoubleClick={onFileDoubleClick}
          />
        ))}
      </div>
    );
  }

  const isSelected = node.fullPath === selectedPath;
  return (
    <button
      onClick={() => { if (node.fullPath) onFileClick(node.fullPath); }}
      onDoubleClick={() => { if (node.fullPath) onFileDoubleClick(node.fullPath); }}
      title={node.fullPath}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        width: "max-content",
        minWidth: "100%",
        padding: `3px 8px 3px ${8 + depth * 16}px`,
        border: "none",
        background: isSelected ? colors.selectedBg : "none",
        color: isSelected ? colors.textLight : colors.text,
        cursor: "pointer",
        fontSize: 12,
        fontFamily: fonts.mono,
        textAlign: "left",
        whiteSpace: "nowrap",
      }}
      onMouseEnter={(e) => { if (!isSelected) e.currentTarget.style.backgroundColor = colors.hoverBg; }}
      onMouseLeave={(e) => { if (!isSelected) e.currentTarget.style.backgroundColor = "transparent"; }}
    >
      <span style={{ width: 10, flexShrink: 0 }} />
      <MemoryFileIcon />
      {node.name}
    </button>
  );
}
