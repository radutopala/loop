import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type EditorView } from "@codemirror/view";
import { useTheme } from "../../ThemeContext";
import { fetchMemoryFiles, fetchMemoryFileContent, saveMemoryFileContent, type MemoryFileInfo } from "../../api/loopApi";
import { fetchGlobalConfig } from "../../api/configApi";
import { FilePanel } from "./FilePanel";
import { MemoryFileList, type TreeNode } from "./MemoryFileList";
import { MemoryFileViewer } from "./MemoryFileViewer";
import { storageGet, storageSet, storageGetJSON, storageSetJSON } from "../../utils/storage";

const TREE_MIN_WIDTH = 120;
const TREE_MAX_WIDTH = 400;
const TREE_DEFAULT_WIDTH = 200;
const TREE_WIDTH_KEY = "loop-memory-tree-width";
const TABS_KEY = "loop-memory-tabs";

function loadTreeWidth(): number {
  const stored = storageGet(TREE_WIDTH_KEY);
  if (stored) {
    const w = parseInt(stored, 10);
    if (w >= TREE_MIN_WIDTH && w <= TREE_MAX_WIDTH) return w;
  }
  return TREE_DEFAULT_WIDTH;
}

interface TabsState { tabs: string[]; selected: string | null; }

function loadTabs(channelId: string): TabsState {
  const all = storageGetJSON<Record<string, TabsState>>(TABS_KEY);
  if (all && typeof all === "object" && all[channelId]) {
    return all[channelId];
  }
  return { tabs: [], selected: null };
}

function saveTabs(channelId: string, state: TabsState) {
  const all = storageGetJSON<Record<string, TabsState>>(TABS_KEY) ?? {};
  if (state.tabs.length > 0) {
    all[channelId] = state;
  } else {
    delete all[channelId];
  }
  storageSetJSON(TABS_KEY, all);
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
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());

  const [openTabs, setOpenTabs] = useState<string[]>(() => loadTabs(channelId).tabs);
  const [selectedPath, setSelectedPath] = useState<string | null>(() => loadTabs(channelId).selected);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [contentError, setContentError] = useState<string | null>(null);
  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [previewTab, setPreviewTab] = useState<string | null>(null);
  const [autoSaveOnBlur, setAutoSaveOnBlur] = useState(false);
  const [previewTabsEnabled, setPreviewTabsEnabled] = useState(true);

  const viewRef = useRef<EditorView | null>(null);
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
      storageSet(TREE_WIDTH_KEY, String(lastWidth));
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

  return (
    <FilePanel title="Memory" dirPath={dirPath} branch={branch} noPadding={!loading && files.length > 0} embedded={embedded} dataTestId="memory-panel" {...panelProps}>
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
          <MemoryFileList
            tree={tree}
            treeWidth={treeWidth}
            treeMinWidth={TREE_MIN_WIDTH}
            treeMaxWidth={TREE_MAX_WIDTH}
            treeResizing={treeResizing}
            expandedDirs={expandedDirs}
            selectedPath={selectedPath}
            onLoadFiles={loadFiles}
            onTreeResize={handleTreeResize}
            onDirToggle={handleDirToggle}
            onFileClick={handleFileClick}
            onFileDoubleClick={handleFileDoubleClick}
          />
          <MemoryFileViewer
            selectedPath={selectedPath}
            fileContent={fileContent}
            contentError={contentError}
            openTabs={openTabs}
            dirtyTabs={dirtyTabs}
            previewTab={previewTab}
            viewRef={viewRef}
            onSwitchToTab={switchToTab}
            onCloseTab={handleCloseTab}
            onSetPreviewTab={setPreviewTab}
            onMarkDirty={markDirty}
            onSaveAllDirty={saveAllDirty}
          />
        </div>
      )}
    </FilePanel>
  );
}
