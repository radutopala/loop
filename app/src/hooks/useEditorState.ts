import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchRoots,
  fetchFiles,
  fetchFileContent,
  saveFileContent,
  deleteFile,
  createDir,
  updateExtraDirs,
  buildFileUrl,
  isImagePath,
  isVideoPath,
  type FileEntry,
  type RootEntry,
} from "../api/loopApi";
import { fetchGlobalConfig } from "../api/configApi";
import { fetchDiff } from "../api/git";
import { makePathKey, parsePathKey } from "../components/panels/EditorFileTree";
import { matchAbsPathToKey } from "./editorPaths";
import { gitLineChangesForFile, emptyGitLineChanges, type GitLineChanges } from "../components/panels/editorGitGutter";
import type { CodeEditorHandle } from "../components/panels/CodeEditor";
import type { ChatEventListener } from "./useChatStateStore";
import type { ToolUseData, WSEvent } from "../types";
import { storageGetJSON, storageSetJSON } from "../utils/storage";

const EDITOR_TABS_KEY = "loop-editor-tabs";

interface EditorTabsState { tabs: string[]; selected: string | null }

function loadEditorTabs(channelId: string, key: string): EditorTabsState {
  const all = storageGetJSON<Record<string, EditorTabsState>>(key);
  if (all && typeof all === "object" && all[channelId]) {
    return all[channelId];
  }
  return { tabs: [], selected: null };
}

function saveEditorTabs(channelId: string, state: EditorTabsState, key: string) {
  const all = storageGetJSON<Record<string, EditorTabsState>>(key) ?? {};
  if (state.tabs.length > 0) {
    all[channelId] = state;
  } else {
    delete all[channelId];
  }
  storageSetJSON(key, all);
}

interface UseEditorStateOptions {
  tabsStorageKey?: string;
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
}

export interface EditorStateApi {
  // File-tree state
  roots: RootEntry[];
  expandedDirs: Set<string>;
  dirContents: Map<string, FileEntry[]>;
  selectedDir: string;

  // Tab state
  openTabs: string[];
  selectedPath: string | null;
  previewTab: string | null;

  // Currently visible file content
  fileContent: string | null;
  isBinary: boolean;
  binarySize: number;
  // For image files: the URL to render in <img src=...>. Null for text /
  // generic binary tabs. Includes a cache-busting query param so agent edits
  // reload the displayed bytes.
  imageURL: string | null;
  loading: boolean;
  error: string | null;

  // VCS change markers (added/modified/deleted lines vs git HEAD) for the
  // currently-open file, fed to the editor's gutter.
  gitChanges: GitLineChanges;

  // Dirty + auto-refresh state
  dirtyTabs: Set<string>;
  pendingRefresh: Map<string, string>;
  autoSaveOnBlur: boolean;
  previewTabsEnabled: boolean;

  // Refs the editor view attaches to
  codeEditorRef: React.RefObject<CodeEditorHandle | null>;

  // Actions on the file tree
  loadDir: (path: string, rootIndex?: number) => Promise<void>;
  refreshTree: () => Promise<void>;
  toggleDir: (pathKey: string) => void;
  setSelectedDir: (pathKey: string) => void;
  addExtraDir: (dir: string) => Promise<void>;
  handleCreateFile: (name: string) => void;
  handleDeleteFilePath: (pathKey: string) => void;
  handleCreateDirPath: (name: string) => void;
  handleDeleteDirPath: (pathKey: string) => void;

  // Actions on tabs/content
  switchToTab: (pathKey: string) => void;
  openFile: (path: string) => void;
  promoteFile: (path: string) => void;
  closeTab: (path: string, e?: React.MouseEvent) => void;
  markDirty: () => void;
  saveFile: (filePath?: string) => void;
  saveAllDirty: () => void;
  clearError: () => void;
  /** Open a file (preview tab) and scroll to the given 1-based line once loaded. */
  openFileAtLine: (path: string, line: number | null) => void;

  // Auto-refresh resolution
  acceptPendingRefresh: (pathKey: string) => void;
  dismissPendingRefresh: (pathKey: string) => void;
}

/**
 * Owns the shared state of the editor + file-tree panels: the open tabs,
 * the active file, the loaded directory contents, and the dirty/auto-refresh
 * bookkeeping. Hoisted in WorkspaceLayout so the file tree and the editor
 * tabs stay in sync as independent panels.
 */
export function useEditorState(channelId: string, options?: UseEditorStateOptions): EditorStateApi {
  const tabsKey = options?.tabsStorageKey ?? EDITOR_TABS_KEY;
  const subscribeChatEvents = options?.subscribeChatEvents;

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
  const [imageURL, setImageURL] = useState<string | null>(null);
  // Counter bumped on agent refresh of the active image tab; appended as `?t=`
  // to the URL so the browser re-fetches instead of serving the cached image.
  const imageVersionRef = useRef(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [gitChanges, setGitChanges] = useState<GitLineChanges>(emptyGitLineChanges);

  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [pendingRefresh, setPendingRefresh] = useState<Map<string, string>>(new Map());
  const [autoSaveOnBlur, setAutoSaveOnBlur] = useState(false);
  const [previewTabsEnabled, setPreviewTabsEnabled] = useState(true);

  const codeEditorRef = useRef<CodeEditorHandle | null>(null);
  const pendingScrollRef = useRef<{ pathKey: string; line: number } | null>(null);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;
  const dirtyTabsRef = useRef(dirtyTabs);
  dirtyTabsRef.current = dirtyTabs;
  const autoSaveOnBlurRef = useRef(autoSaveOnBlur);
  autoSaveOnBlurRef.current = autoSaveOnBlur;
  const expandedDirsRef = useRef(expandedDirs);
  expandedDirsRef.current = expandedDirs;
  const rootsRef = useRef(roots);
  rootsRef.current = roots;
  const dirtyContentRef = useRef(new Map<string, string>());

  // Load desktop settings.
  useEffect(() => {
    fetchGlobalConfig().then((cfg) => {
      const d = cfg.content?.desktop;
      if (!d) return;
      if (typeof d.auto_save_on_blur === "boolean") setAutoSaveOnBlur(d.auto_save_on_blur);
      if (typeof d.preview_tabs === "boolean") setPreviewTabsEnabled(d.preview_tabs);
    }).catch(() => {});
  }, []);

  // Persist tab list (excluding preview tab).
  useEffect(() => {
    const persistedTabs = previewTab ? openTabs.filter((t) => t !== previewTab) : openTabs;
    const persistedSelected = selectedPath === previewTab ? null : selectedPath;
    saveEditorTabs(channelId, { tabs: persistedTabs, selected: persistedSelected }, tabsKey);
  }, [channelId, openTabs, selectedPath, previewTab, tabsKey]);

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
      /* directory may not exist */
    }
  }, [channelId]);

  // Load roots on mount.
  useEffect(() => {
    fetchRoots(channelId).then((r) => {
      setRoots(r);
      setExpandedDirs((prev) => {
        const next = new Set(prev);
        for (const root of r) next.add(makePathKey(root.index, ""));
        return next;
      });
      for (const root of r) loadDir(".", root.index);
    }).catch(() => {
      loadDir(".", 0);
    });
  }, [channelId, loadDir]);

  // On mount, if a tab was persisted, fetch its content.
  useEffect(() => {
    if (!selectedPath) return;
    const { rootIndex: ri, relativePath: rp } = parsePathKey(selectedPath);
    if (isImagePath(rp) || isVideoPath(rp)) {
      setImageURL(buildFileUrl(channelId, rp, ri, imageVersionRef.current));
      setFileContent(null);
      setIsBinary(false);
      return;
    }
    setLoading(true);
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

  // Fetch the open file's uncommitted diff and recompute its gutter markers.
  // Cheap and idempotent; called on file open, save, agent edit, and focus.
  const refreshGitChanges = useCallback(async () => {
    const pathKey = selectedPathRef.current;
    if (!pathKey) {
      setGitChanges(emptyGitLineChanges());
      return;
    }
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    if (isImagePath(rp) || isVideoPath(rp)) {
      setGitChanges(emptyGitLineChanges());
      return;
    }
    try {
      const diff = await fetchDiff(channelId, undefined, undefined, ri);
      if (selectedPathRef.current !== pathKey) return;
      const combined = [diff.staged_diff, diff.unstaged_diff, diff.untracked_diff].filter(Boolean).join("\n");
      setGitChanges(gitLineChangesForFile(combined, rp));
    } catch {
      setGitChanges(emptyGitLineChanges());
    }
  }, [channelId]);

  const refreshGitChangesRef = useRef(refreshGitChanges);
  refreshGitChangesRef.current = refreshGitChanges;

  // Recompute gutter markers whenever the active file (or channel) changes.
  useEffect(() => {
    refreshGitChangesRef.current();
  }, [selectedPath, channelId]);

  const markDirty = useCallback(() => {
    const p = selectedPathRef.current;
    if (!p) return;
    setDirtyTabs((prev) => { if (prev.has(p)) return prev; const next = new Set(prev); next.add(p); return next; });
    setPreviewTab((cur) => cur === p ? null : cur);
    // Editing also implicitly dismisses any pending agent refresh for this tab.
    setPendingRefresh((prev) => {
      if (!prev.has(p)) return prev;
      const next = new Map(prev);
      next.delete(p);
      return next;
    });
  }, []);

  const saveFile = useCallback((filePath?: string) => {
    const savePath = filePath ?? selectedPathRef.current;
    if (!savePath) return;
    const editor = codeEditorRef.current;
    if (!editor || savePath !== selectedPathRef.current) return;
    editor.appendNewlineIfMissing();
    const content = editor.getContent();
    if (content === null) return;
    const { rootIndex: ri, relativePath: rp } = parsePathKey(savePath);
    saveFileContent(channelId, rp, content, ri).then(() => {
      dirtyContentRef.current.delete(savePath);
      setDirtyTabs((prev) => { if (!prev.has(savePath)) return prev; const next = new Set(prev); next.delete(savePath); return next; });
      if (savePath === selectedPathRef.current) refreshGitChangesRef.current();
    }).catch(() => {});
  }, [channelId]);

  const saveAllDirty = useCallback(() => {
    if (dirtyTabsRef.current.has(selectedPathRef.current ?? "")) saveFile();
  }, [saveFile]);

  const switchToTab = useCallback((pathKey: string) => {
    const curPath = selectedPathRef.current;
    if (curPath && dirtyTabsRef.current.has(curPath)) {
      const editor = codeEditorRef.current;
      if (editor) {
        const content = editor.getContent();
        if (content !== null) dirtyContentRef.current.set(curPath, content);
      }
      if (autoSaveOnBlurRef.current) saveAllDirty();
    }
    setSelectedPath(pathKey);
    setError(null);
    setIsBinary(false);
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    if (isImagePath(rp) || isVideoPath(rp)) {
      setImageURL(buildFileUrl(channelId, rp, ri, imageVersionRef.current));
      setFileContent(null);
      setLoading(false);
      return;
    }
    setImageURL(null);
    const cached = dirtyContentRef.current.get(pathKey);
    if (cached !== undefined) {
      setFileContent(cached);
      return;
    }
    setLoading(true);
    setFileContent(null);
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
  }, [channelId, saveAllDirty]);

  // Single-click open: preview tab if enabled, else permanent.
  const openFile = useCallback((path: string) => {
    if (!previewTabsEnabled) {
      setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
      if (selectedPathRef.current !== path) switchToTab(path);
      return;
    }
    if (openTabs.includes(path) && path !== previewTab) {
      if (selectedPathRef.current !== path) switchToTab(path);
      return;
    }
    setOpenTabs((prev) => {
      const without = previewTab ? prev.filter((t) => t !== previewTab) : prev;
      return without.includes(path) ? without : [...without, path];
    });
    setPreviewTab(path);
    if (selectedPathRef.current !== path) switchToTab(path);
  }, [previewTab, previewTabsEnabled, openTabs, switchToTab]);

  const tryFlushPendingScroll = useCallback(() => {
    const pending = pendingScrollRef.current;
    if (!pending) return;
    if (pending.pathKey !== selectedPathRef.current) return;
    const editor = codeEditorRef.current;
    if (!editor) return;
    editor.scrollToLine(pending.line);
    pendingScrollRef.current = null;
  }, []);

  // After file content arrives and CodeEditor mounts, flush any pending scroll.
  useEffect(() => {
    if (fileContent === null) return;
    // CodeEditor mounts on the same render that sets fileContent; the ref is
    // assigned during commit, so this effect (post-commit) sees it. Defer to
    // the next animation frame to ensure layout has settled before scrolling.
    const id = requestAnimationFrame(() => tryFlushPendingScroll());
    return () => cancelAnimationFrame(id);
  }, [fileContent, selectedPath, tryFlushPendingScroll]);

  const fileContentRef = useRef(fileContent);
  fileContentRef.current = fileContent;

  const openFileAtLine = useCallback((path: string, line: number | null) => {
    if (line !== null) {
      pendingScrollRef.current = { pathKey: path, line };
    } else {
      pendingScrollRef.current = null;
    }
    openFile(path);
    // If it's already the active tab with content loaded, openFile is a no-op
    // and no fileContent effect will fire — scroll synchronously.
    if (line !== null && selectedPathRef.current === path && fileContentRef.current !== null) {
      tryFlushPendingScroll();
    }
  }, [openFile, tryFlushPendingScroll]);

  // Double-click promote: ensure permanent and active.
  const promoteFile = useCallback((path: string) => {
    setOpenTabs((prev) => prev.includes(path) ? prev : [...prev, path]);
    if (previewTab === path) setPreviewTab(null);
    if (selectedPathRef.current !== path) switchToTab(path);
  }, [previewTab, switchToTab]);

  const closeTab = useCallback((path: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (autoSaveOnBlurRef.current && path === selectedPathRef.current) saveAllDirty();
    dirtyContentRef.current.delete(path);
    setDirtyTabs((prev) => { if (!prev.has(path)) return prev; const next = new Set(prev); next.delete(path); return next; });
    setPendingRefresh((prev) => { if (!prev.has(path)) return prev; const next = new Map(prev); next.delete(path); return next; });
    if (previewTab === path) setPreviewTab(null);
    setOpenTabs((prev) => {
      const next = prev.filter((p) => p !== path);
      if (path === selectedPathRef.current) {
        if (next.length > 0) {
          const idx = Math.min(prev.indexOf(path), next.length - 1);
          switchToTab(next[Math.max(0, idx)]!);
        } else {
          setSelectedPath(null);
          setFileContent(null);
          setIsBinary(false);
          setImageURL(null);
          setError(null);
        }
      }
      return next;
    });
  }, [previewTab, saveAllDirty, switchToTab]);

  const refreshTree = useCallback(async () => {
    try {
      const r = await fetchRoots(channelId);
      setRoots(r);
      for (const root of r) loadDir(".", root.index);
    } catch {
      loadDir(".", 0);
    }
    for (const dirKey of expandedDirsRef.current) {
      const { rootIndex, relativePath } = parsePathKey(dirKey);
      loadDir(relativePath === "" ? "." : relativePath, rootIndex);
    }
    const pathKey = selectedPathRef.current;
    if (pathKey) {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      if (isImagePath(rp) || isVideoPath(rp)) {
        imageVersionRef.current++;
        setImageURL(buildFileUrl(channelId, rp, ri, imageVersionRef.current));
        return;
      }
      try {
        const result = await fetchFileContent(channelId, rp, ri);
        if (selectedPathRef.current !== pathKey || result.binary) return;
        const editor = codeEditorRef.current;
        if (!editor) return;
        const current = editor.getContent();
        if (current !== null && result.content !== current) {
          editor.replaceContent(result.content);
          setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
        }
      } catch { /* file may have been deleted */ }
    }
  }, [loadDir, channelId]);

  const toggleDir = useCallback((pathKey: string) => {
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

  const handleCreateFile = useCallback((name: string) => {
    const trimmed = name.trim();
    if (!trimmed) return;
    const { rootIndex } = parsePathKey(selectedDir);
    const { rootIndex: ri, relativePath: rp } = parsePathKey(trimmed);
    const actualRoot = trimmed.includes(":") ? ri : rootIndex;
    const actualPath = trimmed.includes(":") ? rp : trimmed;
    saveFileContent(channelId, actualPath, "", actualRoot).then(() => {
      const parentRelPath = actualPath.includes("/") ? actualPath.substring(0, actualPath.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, actualRoot);
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
      setOpenTabs((prev) => {
        const next = prev.filter((p) => p !== pathKey);
        if (pathKey === selectedPathRef.current) {
          if (next.length > 0) {
            switchToTab(next[Math.max(0, Math.min(prev.indexOf(pathKey), next.length - 1))]!);
          } else {
            setSelectedPath(null);
            setFileContent(null);
            setIsBinary(false);
            setImageURL(null);
            setError(null);
          }
        }
        return next;
      });
      dirtyContentRef.current.delete(pathKey);
      setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
      const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, ri);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to delete file");
    });
  }, [channelId, loadDir, switchToTab]);

  const handleCreateDirPath = useCallback((name: string) => {
    const trimmed = name.trim();
    if (!trimmed) return;
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
      setOpenTabs((prev) => {
        const next = prev.filter((p) => p !== pathKey && !p.startsWith(pathKey + "/"));
        const cur = selectedPathRef.current;
        if (cur && (cur === pathKey || cur.startsWith(pathKey + "/"))) {
          if (next.length > 0) {
            switchToTab(next[0]!);
          } else {
            setSelectedPath(null);
            setFileContent(null);
            setIsBinary(false);
            setImageURL(null);
            setError(null);
          }
        }
        return next;
      });
      const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
      loadDir(parentRelPath, ri);
    }).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to delete directory");
    });
  }, [channelId, loadDir, switchToTab]);

  const addExtraDir = useCallback(async (dir: string) => {
    const currentExtras = rootsRef.current.filter((r) => r.index > 0).map((r) => r.path);
    await updateExtraDirs(channelId, [...currentExtras, dir]);
    const r = await fetchRoots(channelId);
    setRoots(r);
    for (const root of r) loadDir(".", root.index);
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      for (const root of r) next.add(makePathKey(root.index, ""));
      return next;
    });
  }, [channelId, loadDir]);

  // Cmd+S — immediate save.
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

  // Save on blur, reload from disk on focus (picks up external edits).
  useEffect(() => {
    const onBlur = () => { if (autoSaveOnBlurRef.current) saveAllDirty(); };
    const onFocus = () => {
      const pathKey = selectedPathRef.current;
      if (!pathKey) return;
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      if (isImagePath(rp) || isVideoPath(rp)) {
        imageVersionRef.current++;
        setImageURL(buildFileUrl(channelId, rp, ri, imageVersionRef.current));
        return;
      }
      fetchFileContent(channelId, rp, ri).then((result) => {
        if (selectedPathRef.current !== pathKey) return;
        if (result.binary) return;
        const editor = codeEditorRef.current;
        if (!editor) return;
        const current = editor.getContent();
        if (current !== null && result.content !== current) {
          editor.replaceContent(result.content);
          setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
        }
        refreshGitChangesRef.current();
      }).catch(() => {});
    };
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);
    return () => { window.removeEventListener("blur", onBlur); window.removeEventListener("focus", onFocus); };
  }, [channelId, saveAllDirty]);

  const openTabsRef = useRef(openTabs);
  openTabsRef.current = openTabs;

  const applyAgentRefresh = useCallback((pathKey: string) => {
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    if (isImagePath(rp) || isVideoPath(rp)) {
      if (pathKey === selectedPathRef.current) {
        imageVersionRef.current++;
        setImageURL(buildFileUrl(channelId, rp, ri, imageVersionRef.current));
      }
      return;
    }
    fetchFileContent(channelId, rp, ri).then((result) => {
      if (result.binary) return;
      const isDirty = dirtyTabsRef.current.has(pathKey);
      if (isDirty) {
        // Stash latest disk content; the user resolves via Replace / Keep mine.
        setPendingRefresh((prev) => {
          const next = new Map(prev);
          next.set(pathKey, result.content);
          return next;
        });
        return;
      }
      if (pathKey === selectedPathRef.current) {
        const editor = codeEditorRef.current;
        if (editor) {
          const current = editor.getContent();
          if (current !== null && current !== result.content) {
            editor.replaceContent(result.content);
          } else if (current === null) {
            setFileContent(result.content);
          }
        } else {
          setFileContent(result.content);
        }
        refreshGitChangesRef.current();
      } else {
        dirtyContentRef.current.delete(pathKey);
      }
    }).catch(() => {});
  }, [channelId]);

  // Auto-refresh on agent Edit/Write/MultiEdit tool events.
  const applyAgentRefreshRef = useRef(applyAgentRefresh);
  applyAgentRefreshRef.current = applyAgentRefresh;
  useEffect(() => {
    if (!subscribeChatEvents) return;
    const handler = (event: WSEvent) => {
      if (event.type !== "tool.use") return;
      const data = event.data as ToolUseData;
      if (data.tool_name !== "Edit" && data.tool_name !== "Write" && data.tool_name !== "MultiEdit") return;
      let parsed: { file_path?: string; edits?: { file_path?: string }[] } | null = null;
      try {
        parsed = JSON.parse(data.input);
      } catch {
        return;
      }
      if (!parsed) return;
      const filePaths: string[] = [];
      if (typeof parsed.file_path === "string") filePaths.push(parsed.file_path);
      if (Array.isArray(parsed.edits)) {
        for (const edit of parsed.edits) {
          if (edit && typeof edit.file_path === "string") filePaths.push(edit.file_path);
        }
      }
      if (filePaths.length === 0) return;
      const seen = new Set<string>();
      for (const abs of filePaths) {
        if (seen.has(abs)) continue;
        seen.add(abs);
        const pathKey = matchAbsPathToKey(abs, rootsRef.current);
        if (!pathKey) continue;
        if (!openTabsRef.current.includes(pathKey)) continue;
        applyAgentRefreshRef.current(pathKey);
      }
    };
    return subscribeChatEvents(handler);
  }, [subscribeChatEvents]);

  const acceptPendingRefresh = useCallback((pathKey: string) => {
    const content = pendingRefresh.get(pathKey);
    if (content === undefined) return;
    setPendingRefresh((prev) => {
      if (!prev.has(pathKey)) return prev;
      const next = new Map(prev);
      next.delete(pathKey);
      return next;
    });
    dirtyContentRef.current.delete(pathKey);
    setDirtyTabs((prev) => { if (!prev.has(pathKey)) return prev; const next = new Set(prev); next.delete(pathKey); return next; });
    if (pathKey === selectedPathRef.current) {
      const editor = codeEditorRef.current;
      if (editor) {
        editor.replaceContent(content);
      } else {
        setFileContent(content);
      }
    }
  }, [pendingRefresh]);

  const dismissPendingRefresh = useCallback((pathKey: string) => {
    setPendingRefresh((prev) => {
      if (!prev.has(pathKey)) return prev;
      const next = new Map(prev);
      next.delete(pathKey);
      return next;
    });
  }, []);

  const clearError = useCallback(() => setError(null), []);

  return {
    roots,
    expandedDirs,
    dirContents,
    selectedDir,
    openTabs,
    selectedPath,
    previewTab,
    fileContent,
    isBinary,
    binarySize,
    imageURL,
    loading,
    error,
    gitChanges,
    dirtyTabs,
    pendingRefresh,
    autoSaveOnBlur,
    previewTabsEnabled,
    codeEditorRef,
    loadDir,
    refreshTree,
    toggleDir,
    setSelectedDir,
    addExtraDir,
    handleCreateFile,
    handleDeleteFilePath,
    handleCreateDirPath,
    handleDeleteDirPath,
    switchToTab,
    openFile,
    openFileAtLine,
    promoteFile,
    closeTab,
    markDirty,
    saveFile,
    saveAllDirty,
    clearError,
    acceptPendingRefresh,
    dismissPendingRefresh,
  };
}

/** Map a tool's absolute file path to a {rootIndex, relativePath} pathKey.
 * Implementation lives in ./editorPaths so it is unit-testable in isolation. */
