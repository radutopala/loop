import "@fontsource/jetbrains-mono/400.css";
import { useCallback, useEffect, useRef, useState } from "react";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from "@codemirror/view";
import { EditorState, Compartment } from "@codemirror/state";
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
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import { fetchFiles, fetchFileContent, saveFileContent, deleteFile, createDir, fetchRoots, updateExtraDirs, type FileEntry, type RootEntry } from "../api/loopApi";
import { FilePanel, buildMarkdownStyles } from "./FilePanel";
import { ContextMenu, type MenuItem } from "./ContextMenu";
import { buildEditorTheme } from "./editorTheme";

interface EditorPanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  tabBar?: React.ReactNode;
  embedded?: boolean;
  tabsStorageKey?: string;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

function getLangExtension(filename: string) {
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

function isMarkdownFile(path: string): boolean {
  const ext = path.split(".").pop()?.toLowerCase();
  return ext === "md" || ext === "mdx";
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

const TREE_MIN_WIDTH = 120;
const TREE_MAX_WIDTH = 500;
const TREE_DEFAULT_WIDTH = 280;
const TREE_WIDTH_KEY = "loop-editor-tree-width";

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

const EDITOR_TABS_KEY = "loop-editor-tabs";

interface EditorTabsState { tabs: string[]; selected: string | null; }

function loadEditorTabs(channelId: string, key = EDITOR_TABS_KEY): EditorTabsState {
  try {
    const stored = localStorage.getItem(key);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        return all[channelId];
      }
    }
  } catch { /* ignore */ }
  return { tabs: [], selected: null };
}

function saveEditorTabs(channelId: string, state: EditorTabsState, key = EDITOR_TABS_KEY) {
  try {
    const stored = localStorage.getItem(key);
    const all = stored ? JSON.parse(stored) : {};
    if (state.tabs.length > 0) {
      all[channelId] = state;
    } else {
      delete all[channelId];
    }
    localStorage.setItem(key, JSON.stringify(all));
  } catch { /* ignore */ }
}

// ── Multi-root path key helpers ──
// Internal path keys use "{rootIndex}:{relativePath}" to disambiguate across roots.
// When there's only one root (index 0), the prefix is still present internally
// but stripped for display / API calls.

function makePathKey(rootIndex: number, relativePath: string): string {
  return `${rootIndex}:${relativePath}`;
}

function parsePathKey(key: string): { rootIndex: number; relativePath: string } {
  const colonIdx = key.indexOf(":");
  if (colonIdx < 0) return { rootIndex: 0, relativePath: key };
  const rootIndex = parseInt(key.substring(0, colonIdx), 10);
  return { rootIndex: isNaN(rootIndex) ? 0 : rootIndex, relativePath: key.substring(colonIdx + 1) };
}

export function EditorPanel({ channelId, dirPath, branch, embedded, tabsStorageKey, ...panelProps }: EditorPanelProps) {
  const { colors } = useTheme();
  const tabsKey = tabsStorageKey ?? EDITOR_TABS_KEY;
  const [roots, setRoots] = useState<RootEntry[]>([]);
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set([makePathKey(0, "")]));
  const [dirContents, setDirContents] = useState<Map<string, FileEntry[]>>(new Map());
  const [selectedDir, setSelectedDir] = useState(makePathKey(0, ""));

  const [openTabs, setOpenTabs] = useState<string[]>(() => loadEditorTabs(channelId, tabsKey).tabs);
  const [selectedPath, setSelectedPath] = useState<string | null>(() => loadEditorTabs(channelId, tabsKey).selected);
  const [previewTab, setPreviewTab] = useState<string | null>(null);

  const [fileContent, setFileContent] = useState<string | null>(null);
  const [isBinary, setIsBinary] = useState(false);
  const [binarySize, setBinarySize] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [treeWidth, setTreeWidth] = useState(loadTreeWidth);
  const [treeResizing, setTreeResizing] = useState(false);
  const [previewMode, setPreviewMode] = useState<"editor" | "both" | "preview">("both");
  const [previewHtml, setPreviewHtml] = useState("");
  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [autoSaveOnBlur, setAutoSaveOnBlur] = useState(true);
  const [newFileName, setNewFileName] = useState<string | null>(null);
  const [newDirName, setNewDirName] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; path: string; isDir: boolean } | null>(null);
  const [editorMenu, setEditorMenu] = useState<{ x: number; y: number } | null>(null);

  const editorRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const themeCompartment = useRef(new Compartment());
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const scrollSyncSource = useRef<"editor" | "preview" | null>(null);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;

  const selectedRelPath = selectedPath ? parsePathKey(selectedPath).relativePath : null;
  const isMd = selectedRelPath ? isMarkdownFile(selectedRelPath) : false;
  const hasMultipleRoots = roots.length > 1;

  const [previewTabsEnabled, setPreviewTabsEnabled] = useState(true);

  // Load settings.
  useEffect(() => {
    window.loopAPI?.getSettings?.().then((s) => {
      if (typeof s.autoSaveOnBlur === "boolean") setAutoSaveOnBlur(s.autoSaveOnBlur);
      if (typeof s.previewTabs === "boolean") setPreviewTabsEnabled(s.previewTabs);
    }).catch(() => {});
  }, []);

  // Persist tab list to localStorage whenever it changes (exclude preview tab).
  useEffect(() => {
    const persistedTabs = previewTab ? openTabs.filter((t) => t !== previewTab) : openTabs;
    const persistedSelected = selectedPath === previewTab ? null : selectedPath;
    saveEditorTabs(channelId, { tabs: persistedTabs, selected: persistedSelected }, tabsKey);
  }, [channelId, openTabs, selectedPath, previewTab, tabsKey]);

  const dirtyTabsRef = useRef(dirtyTabs);
  dirtyTabsRef.current = dirtyTabs;
  const autoSaveOnBlurRef = useRef(autoSaveOnBlur);
  autoSaveOnBlurRef.current = autoSaveOnBlur;
  // Cache unsaved content so dirty tabs survive tab switches.
  const dirtyContentRef = useRef(new Map<string, string>());

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
    // If saving a non-active tab we don't have its content — skip.
    if (!view || savePath !== selectedPathRef.current) return;
    let content = view.state.doc.toString();
    if (content.length > 0 && !content.endsWith("\n")) {
      content += "\n";
      view.dispatch({ changes: { from: view.state.doc.length, insert: "\n" } });
    }
    const { rootIndex: ri, relativePath: rp } = parsePathKey(savePath);
    saveFileContent(channelId, rp, content, ri).then(() => {
      dirtyContentRef.current.delete(savePath);
      setDirtyTabs((prev) => { if (!prev.has(savePath)) return prev; const next = new Set(prev); next.delete(savePath); return next; });
    }).catch(() => {});
  }, [channelId]);

  const saveAllDirty = useCallback(() => {
    // Only the active tab can be saved (we have its view).
    if (dirtyTabsRef.current.has(selectedPathRef.current ?? "")) {
      saveFile();
    }
  }, [saveFile]);

  // Save on unmount only if auto-save on blur is enabled.
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

  // On mount: if we have a selected path, load it from server.
  useEffect(() => {
    if (!selectedPath) return;
    setLoading(true);
    const { rootIndex: ri, relativePath: rp } = parsePathKey(selectedPath);
    fetchFileContent(channelId, rp, ri).then((result) => {
      if (result.binary) {
        setIsBinary(true);
        setBinarySize(0);
        setFileContent(null);
      } else {
        setFileContent(result.content);
      }
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to load file");
    }).finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

  // Load roots and root directories on mount.
  useEffect(() => {
    fetchRoots(channelId).then((r) => {
      setRoots(r);
      // Initialize expanded dirs to include all root keys.
      setExpandedDirs((prev) => {
        const next = new Set(prev);
        for (const root of r) {
          next.add(makePathKey(root.index, ""));
        }
        return next;
      });
      // Load each root's contents.
      for (const root of r) {
        loadDir(".", root.index);
      }
    }).catch(() => {
      // Fallback: load single root.
      loadDir(".", 0);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  const loadDir = useCallback(async (path: string, rootIndex = 0) => {
    try {
      const entries = await fetchFiles(channelId, path, rootIndex);
      const mapKey = makePathKey(rootIndex, path === "." ? "" : path);
      setDirContents((prev) => {
        const next = new Map(prev);
        next.set(mapKey, entries);
        return next;
      });
    } catch {
      /* ignore - directory may not exist */
    }
  }, [channelId]);

  const refreshTree = useCallback(async () => {
    // Reload roots.
    try {
      const r = await fetchRoots(channelId);
      setRoots(r);
      for (const root of r) {
        loadDir(".", root.index);
      }
    } catch {
      loadDir(".", 0);
    }
    // Reload all expanded directories.
    for (const dirKey of expandedDirs) {
      const { rootIndex, relativePath } = parsePathKey(dirKey);
      loadDir(relativePath === "" ? "." : relativePath, rootIndex);
    }
    // Reload the currently open file from disk.
    const pathKey = selectedPathRef.current;
    if (pathKey) {
      try {
        const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
        const result = await fetchFileContent(channelId, rp, ri);
        if (selectedPathRef.current !== pathKey || result.binary) return;
        const view = viewRef.current;
        if (!view) return;
        const current = view.state.doc.toString();
        if (result.content !== current) {
          view.dispatch({ changes: { from: 0, to: current.length, insert: result.content } });
          setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
        }
      } catch { /* file may have been deleted */ }
    }
  }, [loadDir, expandedDirs, channelId]);

  const handleDirClick = useCallback((pathKey: string) => {
    setSelectedDir(pathKey);
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      if (next.has(pathKey)) {
        next.delete(pathKey);
      } else {
        next.add(pathKey);
        if (!dirContents.has(pathKey)) {
          const { rootIndex, relativePath } = parsePathKey(pathKey);
          loadDir(relativePath === "" ? "." : relativePath, rootIndex);
        }
      }
      return next;
    });
  }, [dirContents, loadDir]);

  const switchToTab = useCallback((pathKey: string) => {
    // Snapshot current dirty content before switching away.
    const curPath = selectedPathRef.current;
    if (curPath && dirtyTabsRef.current.has(curPath)) {
      const view = viewRef.current;
      if (view) dirtyContentRef.current.set(curPath, view.state.doc.toString());
      // Auto-save if enabled.
      if (autoSaveOnBlurRef.current) saveAllDirty();
    }
    setSelectedPath(pathKey);
    setError(null);
    setIsBinary(false);
    // Restore from dirty cache if available, otherwise fetch from disk.
    const cached = dirtyContentRef.current.get(pathKey);
    if (cached !== undefined) {
      setFileContent(cached);
    } else {
      setLoading(true);
      setFileContent(null);
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      fetchFileContent(channelId, rp, ri).then((result) => {
        if (result.binary) {
          setIsBinary(true);
          setBinarySize(0);
          setFileContent(null);
        } else {
          setFileContent(result.content);
        }
      }).catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to load file");
      }).finally(() => {
        setLoading(false);
      });
    }
  }, [channelId, saveAllDirty]);

  // Single-click: open file in a preview (transient) tab, or permanently if preview is disabled.
  const handleFileClick = useCallback((path: string, _entry: FileEntry) => {
    if (!previewTabsEnabled) {
      // Preview disabled — open permanently like before.
      setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
      if (selectedPath !== path) switchToTab(path);
      return;
    }
    // If already a permanent tab, just switch to it.
    if (openTabs.includes(path) && path !== previewTab) {
      if (selectedPath !== path) switchToTab(path);
      return;
    }
    // Replace the existing preview tab with this file.
    setOpenTabs((prev) => {
      const without = previewTab ? prev.filter((t) => t !== previewTab) : prev;
      return without.includes(path) ? without : [...without, path];
    });
    setPreviewTab(path);
    if (selectedPath !== path) switchToTab(path);
  }, [selectedPath, previewTab, previewTabsEnabled, openTabs, switchToTab]);

  // Double-click: promote preview to permanent or open permanently.
  const handleFileDoubleClick = useCallback((path: string, _entry: FileEntry) => {
    // Ensure it's in the tab list.
    setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
    // Promote from preview to permanent.
    if (previewTab === path) setPreviewTab(null);
    if (selectedPath !== path) switchToTab(path);
  }, [selectedPath, previewTab, switchToTab]);

  const handleCloseTab = useCallback((path: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    // Auto-save before closing (only if auto-save enabled).
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
          setIsBinary(false);
          setError(null);
        }
      }
      return next;
    });
  }, [selectedPath, saveAllDirty, switchToTab]);

  const handleCreateFile = useCallback((name: string) => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setNewFileName(null);
    // Determine root index from the selected directory.
    const { rootIndex } = parsePathKey(selectedDir);
    const { rootIndex: ri, relativePath: rp } = parsePathKey(trimmed);
    // If the name was typed without a root prefix, use the selected dir's root.
    const actualRoot = trimmed.includes(":") ? ri : rootIndex;
    const actualPath = trimmed.includes(":") ? rp : trimmed;
    saveFileContent(channelId, actualPath, "", actualRoot).then(() => {
      // Reload parent directory to show the new file.
      const parentRelPath = actualPath.includes("/") ? actualPath.substring(0, actualPath.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, actualRoot);
      // Open the new file in a tab.
      const newKey = makePathKey(actualRoot, actualPath);
      setOpenTabs((prev) => prev.includes(newKey) ? prev : [...prev, newKey]);
      switchToTab(newKey);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to create file");
    });
  }, [channelId, loadDir, switchToTab, selectedDir]);

  const handleDeleteFilePath = useCallback((pathKey: string) => {
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    deleteFile(channelId, rp, ri).then(() => {
      // Close the tab if it was open.
      setOpenTabs((prev) => {
        const next = prev.filter((p) => p !== pathKey);
        if (pathKey === selectedPathRef.current) {
          if (next.length > 0) {
            switchToTab(next[Math.max(0, Math.min(prev.indexOf(pathKey), next.length - 1))]!);
          } else {
            setSelectedPath(null);
            setFileContent(null);
            setIsBinary(false);
            setError(null);
          }
        }
        return next;
      });
      dirtyContentRef.current.delete(pathKey);
      setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
      // Reload parent directory.
      const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, ri);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to delete file");
    });
  }, [channelId, loadDir, switchToTab]);

  const handleCreateDirPath = useCallback((name: string) => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setNewDirName(null);
    const { rootIndex } = parsePathKey(selectedDir);
    const { rootIndex: ri, relativePath: rp } = parsePathKey(trimmed);
    const actualRoot = trimmed.includes(":") ? ri : rootIndex;
    const actualPath = trimmed.includes(":") ? rp : trimmed;
    createDir(channelId, actualPath, actualRoot).then(() => {
      const parentRelPath = actualPath.includes("/") ? actualPath.substring(0, actualPath.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, actualRoot);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to create directory");
    });
  }, [channelId, loadDir, selectedDir]);

  const handleDeleteDirPath = useCallback((pathKey: string) => {
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    deleteFile(channelId, rp, ri).then(() => {
      // Close any tabs whose path starts with this directory.
      setOpenTabs((prev) => {
        const next = prev.filter((p) => p !== pathKey && !p.startsWith(pathKey + "/"));
        if (selectedPathRef.current && (selectedPathRef.current === pathKey || selectedPathRef.current.startsWith(pathKey + "/"))) {
          if (next.length > 0) {
            switchToTab(next[0]!);
          } else {
            setSelectedPath(null);
            setFileContent(null);
            setIsBinary(false);
            setError(null);
          }
        }
        return next;
      });
      // Reload parent directory.
      const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, ri);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to delete directory");
    });
  }, [channelId, loadDir, switchToTab]);

  const handleContextMenu = useCallback((e: React.MouseEvent, path: string, isDir: boolean) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, path, isDir });
  }, []);

  const getContextMenuItems = useCallback((): MenuItem[] => {
    if (!contextMenu) return [];
    const { rootIndex: ctxRoot, relativePath: ctxRelPath } = parsePathKey(contextMenu.path);
    const ctxRootEntry = roots.find((r) => r.index === ctxRoot);
    const absBase = ctxRootEntry?.path ?? dirPath;
    const items: MenuItem[] = [];
    items.push({ label: "Copy relative path", onClick: () => navigator.clipboard.writeText(ctxRelPath) });
    items.push({ label: "Copy absolute path", onClick: () => navigator.clipboard.writeText(absBase + "/" + ctxRelPath) });
    if (contextMenu.isDir) {
      const prefix = ctxRelPath && ctxRelPath !== "." ? ctxRelPath + "/" : "";
      items.push({ label: "New file here", separator: true, onClick: () => setNewFileName(prefix) });
      items.push({ label: "New directory here", onClick: () => setNewDirName(prefix) });
      items.push({ label: "Delete", danger: true, separator: true, onClick: () => handleDeleteDirPath(contextMenu.path) });
    } else {
      items.push({ label: "Delete", danger: true, separator: true, onClick: () => handleDeleteFilePath(contextMenu.path) });
    }
    return items;
  }, [contextMenu, dirPath, roots, handleDeleteFilePath, handleDeleteDirPath]);

  const handleEditorContextMenu = useCallback((e: React.MouseEvent) => {
    // Only show custom menu when right-clicking inside the CodeMirror editor area.
    const target = e.target as HTMLElement;
    if (!target.closest(".cm-editor")) return;
    e.preventDefault();
    setEditorMenu({ x: e.clientX, y: e.clientY });
  }, []);

  const getEditorMenuItems = useCallback((): MenuItem[] => {
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
  }, []);

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
          if (selectedPathRef.current) {
            const { relativePath: curRel } = parsePathKey(selectedPathRef.current);
            if (isMarkdownFile(curRel)) {
              updatePreview(update.state.doc.toString());
            }
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
      setPreviewHtml(marked.parse(fileContent, { async: false }) as string);
    } else {
      setPreviewHtml("");
    }

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
  }, [fileContent, isBinary, selectedPath]);

  // Reconfigure CodeMirror theme when the palette changes.
  useEffect(() => {
    if (viewRef.current) {
      viewRef.current.dispatch({
        effects: themeCompartment.current.reconfigure(buildEditorTheme(colors)),
      });
    }
  }, [colors]);

  // Cmd+S keyboard shortcut — immediate save.
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

  // Save on blur (if enabled), reload from disk on focus (picks up external edits).
  useEffect(() => {
    const onBlur = () => { if (autoSaveOnBlur) saveAllDirty(); };
    const onFocus = () => {
      const pathKey = selectedPathRef.current;
      if (!pathKey) return;
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      fetchFileContent(channelId, rp, ri).then((result) => {
        if (selectedPathRef.current !== pathKey) return;
        if (result.binary) return;
        const view = viewRef.current;
        if (!view) return;
        const current = view.state.doc.toString();
        if (result.content !== current) {
          view.dispatch({
            changes: { from: 0, to: current.length, insert: result.content },
          });
          setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
          if (isMarkdownFile(rp)) {
            setPreviewHtml(marked.parse(result.content, { async: false }) as string);
          }
        }
      }).catch(() => { /* ignore — file may have been deleted */ });
    };
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);
    return () => { window.removeEventListener("blur", onBlur); window.removeEventListener("focus", onFocus); };
  }, [channelId, saveAllDirty, autoSaveOnBlur]);

  return (
    <FilePanel title="Editor" dirPath={dirPath} branch={branch} noPadding embedded={embedded} {...panelProps}>
      <div style={{ display: "flex", height: "100%", userSelect: treeResizing ? "none" : undefined }}>
        {/* File tree */}
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
            <span style={{ flex: 1, fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Workspace</span>
            <button
              onClick={refreshTree}
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
            <button
              onClick={() => {
                const { relativePath: selRel } = parsePathKey(selectedDir);
                setNewFileName(selRel ? selRel + "/" : "");
              }}
              title="New file"
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center", marginLeft: 4 }}
              onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
              onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
            </button>
            {window.loopAPI?.showOpenDirectoryDialog && (
              <button
                onClick={async () => {
                  const dir = await window.loopAPI?.showOpenDirectoryDialog?.();
                  if (!dir) return;
                  const currentExtras = roots.filter((r) => r.index > 0).map((r) => r.path);
                  try {
                    await updateExtraDirs(channelId, [...currentExtras, dir]);
                    const r = await fetchRoots(channelId);
                    setRoots(r);
                    for (const root of r) {
                      loadDir(".", root.index);
                    }
                    setExpandedDirs((prev) => {
                      const next = new Set(prev);
                      for (const root of r) {
                        next.add(makePathKey(root.index, ""));
                      }
                      return next;
                    });
                  } catch (err) {
                    setError(err instanceof Error ? err.message : "Failed to add directory");
                  }
                }}
                title="Add directory"
                style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center", marginLeft: 4 }}
                onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                  <line x1="12" y1="11" x2="12" y2="17" />
                  <line x1="9" y1="14" x2="15" y2="14" />
                </svg>
              </button>
            )}
          </div>
          {newFileName !== null && (
            <div style={{ padding: "2px 8px" }}>
              <input
                autoFocus
                placeholder="path/to/file.ext"
                value={newFileName}
                onChange={(e) => setNewFileName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleCreateFile(newFileName);
                  if (e.key === "Escape") setNewFileName(null);
                }}
                onBlur={() => setNewFileName(null)}
                style={{
                  width: "100%",
                  boxSizing: "border-box",
                  background: colors.bg,
                  border: `1px solid ${colors.active}`,
                  color: colors.textLight,
                  fontSize: 11,
                  fontFamily: fonts.mono,
                  padding: "2px 4px",
                  borderRadius: 3,
                  outline: "none",
                }}
              />
            </div>
          )}
          {newDirName !== null && (
            <div style={{ padding: "2px 8px" }}>
              <input
                autoFocus
                placeholder="path/to/directory"
                value={newDirName}
                onChange={(e) => setNewDirName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleCreateDirPath(newDirName);
                  if (e.key === "Escape") setNewDirName(null);
                }}
                onBlur={() => setNewDirName(null)}
                style={{
                  width: "100%",
                  boxSizing: "border-box",
                  background: colors.bg,
                  border: `1px solid ${colors.warning}`,
                  color: colors.textLight,
                  fontSize: 11,
                  fontFamily: fonts.mono,
                  padding: "2px 4px",
                  borderRadius: 3,
                  outline: "none",
                }}
              />
            </div>
          )}
          <div style={{ flex: 1, overflow: "auto", padding: "2px 0" }}>
            {(roots.length > 0 ? roots : [{ index: 0, path: dirPath, name: dirPath.split("/").pop() || dirPath }]).map((root) => {
              const rootKey = makePathKey(root.index, "");
              const isRootExpanded = expandedDirs.has(rootKey);
              const isRootSelected = selectedDir === rootKey;
              return (
                <div key={root.index}>
                  <button
                    onClick={() => {
                      setSelectedDir(rootKey);
                      setExpandedDirs((prev) => {
                        const next = new Set(prev);
                        if (next.has(rootKey)) {
                          next.delete(rootKey);
                        } else {
                          next.add(rootKey);
                          if (!dirContents.has(rootKey)) loadDir(".", root.index);
                        }
                        return next;
                      });
                    }}
                    onContextMenu={(e) => handleContextMenu(e, rootKey, true)}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 4,
                      width: "max-content",
                      minWidth: "100%",
                      padding: "3px 8px",
                      border: "none",
                      background: isRootSelected ? colors.dirSelectedBg : "none",
                      color: colors.textLight,
                      cursor: "pointer",
                      fontSize: 12,
                      fontFamily: fonts.mono,
                      fontWeight: 700,
                      textAlign: "left",
                      whiteSpace: "nowrap",
                    }}
                    onMouseEnter={(e) => { if (!isRootSelected) e.currentTarget.style.backgroundColor = colors.hoverBg; }}
                    onMouseLeave={(e) => { if (!isRootSelected) e.currentTarget.style.backgroundColor = "transparent"; }}
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5, transform: isRootExpanded ? "rotate(90deg)" : "rotate(0deg)", transition: "transform 0.15s" }}>
                      <polyline points="9 18 15 12 9 6" />
                    </svg>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
                      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                    </svg>
                    {root.name}
                  </button>
                  {isRootExpanded && <FileTree
                    entries={dirContents.get(rootKey) || []}
                    dirContents={dirContents}
                    expandedDirs={expandedDirs}
                    selectedPath={selectedPath}
                    previewTab={previewTab}
                    selectedDir={selectedDir}
                    depth={1}
                    parentPath={rootKey}
                    rootIndex={root.index}
                    onDirClick={handleDirClick}
                    onFileClick={handleFileClick}
                    onFileDoubleClick={handleFileDoubleClick}
                    onContextMenu={handleContextMenu}
                  />}
                </div>
              );
            })}
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
        {/* Editor area */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
          {/* Open file tabs */}
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
                {openTabs.map((tab) => {
                  const isActive = tab === selectedPath;
                  const isDirty = dirtyTabs.has(tab);
                  const isPreview = tab === previewTab;
                  const { rootIndex: tabRoot, relativePath: tabRelPath } = parsePathKey(tab);
                  const fileName = tabRelPath.split("/").pop() || tabRelPath;
                  const tabRootEntry = roots.find((r) => r.index === tabRoot);
                  const tabLabel = hasMultipleRoots && tabRootEntry ? `${tabRootEntry.name}/${fileName}` : fileName;
                  return (
                    <button
                      key={tab}
                      onClick={() => { if (!isActive) switchToTab(tab); }}
                      onDoubleClick={() => { if (isPreview) setPreviewTab(null); }}
                      title={tabRelPath}
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
                      <FileIcon name={fileName} />
                      <span style={{ fontStyle: isPreview || isDirty ? "italic" : undefined }}>{tabLabel}</span>
                      <span
                        onClick={(e) => handleCloseTab(tab, e)}
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
              {isMd && (
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
              )}
            </div>
          )}
          {loading && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Loading...</div>
          )}
          {error && (
            <div style={{ padding: 16, color: colors.error, fontSize: 13 }}>{error}</div>
          )}
          {isBinary && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>
              Binary file ({formatSize(binarySize)})
            </div>
          )}
          {!selectedPath && !loading && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Select a file</div>
          )}
          <div style={{ flex: 1, display: fileContent !== null && !isBinary ? "flex" : "none", overflow: "hidden" }} onContextMenu={handleEditorContextMenu}>
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
        </div>
      </div>
      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={getContextMenuItems()}
          onClose={() => setContextMenu(null)}
        />
      )}
      {editorMenu && (
        <ContextMenu
          x={editorMenu.x}
          y={editorMenu.y}
          items={getEditorMenuItems()}
          onClose={() => setEditorMenu(null)}
        />
      )}
    </FilePanel>
  );
}

// ── File Icons ──

function fileIcon(name: string): { color: string; label: string } {
  const ext = name.split(".").pop()?.toLowerCase() || "";
  switch (ext) {
    case "go": return { color: "#00add8", label: "Go" };
    case "ts": case "tsx": return { color: "#3178c6", label: "TS" };
    case "js": case "jsx": case "mjs": case "cjs": return { color: "#f7df1e", label: "JS" };
    case "py": return { color: "#3776ab", label: "Py" };
    case "rs": return { color: "#dea584", label: "Rs" };
    case "json": case "jsonl": return { color: "#cbcb41", label: "{}" };
    case "yaml": case "yml": return { color: "#cb171e", label: "Y" };
    case "toml": return { color: "#9c4221", label: "T" };
    case "md": case "mdx": return { color: "#519aba", label: "M" };
    case "html": case "htm": return { color: "#e34c26", label: "<>" };
    case "css": case "scss": case "less": return { color: "#563d7c", label: "#" };
    case "svg": return { color: "#ffb13b", label: "S" };
    case "sh": case "bash": case "zsh": return { color: "#89e051", label: "$" };
    case "sql": return { color: "#e38c00", label: "Q" };
    case "mod": return { color: "#00add8", label: "Go" };
    case "sum": return { color: "#00add8", label: "Go" };
    case "dockerfile": return { color: "#384d54", label: "D" };
    case "makefile": return { color: "#6d8086", label: "M" };
    case "txt": case "log": case "out": return { color: "#6d8086", label: "" };
    case "png": case "jpg": case "jpeg": case "gif": case "webp": case "ico": return { color: "#a074c4", label: "I" };
    default: break;
  }
  const lower = name.toLowerCase();
  if (lower === "makefile") return { color: "#6d8086", label: "M" };
  if (lower === "dockerfile") return { color: "#384d54", label: "D" };
  if (lower === "license") return { color: "#d4930d", label: "L" };
  if (lower.startsWith(".git")) return { color: "#f14e32", label: "G" };
  if (lower.startsWith(".env")) return { color: "#ecd53f", label: "E" };
  return { color: "", label: "" };
}

function FileIcon({ name }: { name: string }) {
  const { colors } = useTheme();
  const info = fileIcon(name);
  const color = info.color || colors.textDim;
  const label = info.label;
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" style={{ flexShrink: 0 }}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <polyline points="14 2 14 8 20 8" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      {label && (
        <text x="12" y="18" textAnchor="middle" fill={color} fontSize="7" fontWeight="bold" fontFamily={fonts.mono}>{label}</text>
      )}
    </svg>
  );
}

// ── File Tree ──

interface FileTreeProps {
  entries: FileEntry[];
  dirContents: Map<string, FileEntry[]>;
  expandedDirs: Set<string>;
  selectedPath: string | null;
  previewTab: string | null;
  selectedDir: string;
  depth: number;
  parentPath: string;
  rootIndex: number;
  onDirClick: (path: string) => void;
  onFileClick: (path: string, entry: FileEntry) => void;
  onFileDoubleClick: (path: string, entry: FileEntry) => void;
  onContextMenu: (e: React.MouseEvent, path: string, isDir: boolean) => void;
}

function FileTree({ entries, dirContents, expandedDirs, selectedPath, previewTab, selectedDir, depth, parentPath, rootIndex, onDirClick, onFileClick, onFileDoubleClick, onContextMenu }: FileTreeProps) {
  const { colors } = useTheme();
  return (
    <>
      {entries.map((entry) => {
        // parentPath is already a root-prefixed key (e.g. "0:" or "0:src").
        // For children, strip the root prefix from parentPath to get the relative parent,
        // then build the child relative path, then re-prefix.
        const { relativePath: parentRel } = parsePathKey(parentPath);
        const childRel = parentRel ? `${parentRel}/${entry.name}` : entry.name;
        const pathKey = makePathKey(rootIndex, childRel);
        const isDir = entry.type === "dir";
        const isExpanded = expandedDirs.has(pathKey);
        const isSelected = pathKey === selectedPath;
        const isDirSelected = isDir && pathKey === selectedDir;

        return (
          <div key={pathKey}>
            <button
              onClick={() => isDir ? onDirClick(pathKey) : onFileClick(pathKey, entry)}
              onDoubleClick={() => { if (!isDir) onFileDoubleClick(pathKey, entry); }}
              onContextMenu={(e) => onContextMenu(e, pathKey, isDir)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 4,
                width: "max-content",
                minWidth: "100%",
                padding: `3px 8px 3px ${8 + depth * 16}px`,
                border: "none",
                background: isSelected ? colors.selectedBg : isDirSelected ? colors.dirSelectedBg : "none",
                color: isSelected || isDirSelected ? colors.textLight : colors.text,
                cursor: "pointer",
                fontSize: 12,
                fontFamily: fonts.mono,
                textAlign: "left",
                whiteSpace: "nowrap",
              }}
              onMouseEnter={(e) => { if (!isSelected && !isDirSelected) e.currentTarget.style.backgroundColor = colors.hoverBg; }}
              onMouseLeave={(e) => { if (!isSelected && !isDirSelected) e.currentTarget.style.backgroundColor = "transparent"; }}
            >
              {isDir ? (
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ flexShrink: 0, opacity: 0.6, transform: isExpanded ? "rotate(90deg)" : "none", transition: "transform 0.1s" }}>
                  <polyline points="3,1 7,5 3,9" />
                </svg>
              ) : (
                <span style={{ width: 10, flexShrink: 0 }} />
              )}
              {isDir ? (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                </svg>
              ) : (
                <FileIcon name={entry.name} />
              )}
              {entry.name}
            </button>
            {isDir && isExpanded && dirContents.has(pathKey) && (
              <FileTree
                entries={dirContents.get(pathKey)!}
                dirContents={dirContents}
                expandedDirs={expandedDirs}
                selectedPath={selectedPath}
                previewTab={previewTab}
                selectedDir={selectedDir}
                depth={depth + 1}
                parentPath={pathKey}
                rootIndex={rootIndex}
                onDirClick={onDirClick}
                onFileClick={onFileClick}
                onFileDoubleClick={onFileDoubleClick}
                onContextMenu={onContextMenu}
              />
            )}
          </div>
        );
      })}
    </>
  );
}
