import { useCallback, useEffect, useRef, useState } from "react";
import { fetchDiff } from "../api/git";
import {
  buildFileUrl,
  CopyTargetExistsError,
  copyFile,
  createDir,
  DEFAULT_EDITOR_CONFIG,
  deleteFile,
  type EditorConfig,
  type FileEntry,
  FileNotFoundError,
  fetchEditorConfig,
  fetchFileContent,
  fetchFiles,
  fetchRoots,
  forgetContentCap,
  isMediaPath,
  type RootEntry,
  renameFile,
  saveFileContent,
  updateExtraDirs,
} from "../api/loopApi";
import type { CodeEditorHandle } from "../components/panels/CodeEditor";
import { makePathKey, parsePathKey } from "../components/panels/EditorFileTree";
import { emptyGitLineChanges, type GitLineChanges, gitLineChangesForFile } from "../components/panels/editorGitGutter";
import { loadTabViews, saveTabViews, type TabView } from "../components/panels/editorTabViews";
import type { ToolResultData, ToolUseData, WSEvent } from "../types";
import { logErr } from "../utils/log";
import { storageGetJSON, storageSetJSON } from "../utils/storage";
import { copyName, editToolFilePath, matchAbsPathToKey, parentDirKey } from "./editorPaths";
import { fileWatchMessage, parseFileWatchChange } from "./fileWatch";
import { renamedPathKey, renameMapKeys, renameSetKeys } from "./renamePath";
import { selectionAfterClose } from "./tabClose";
import type { ChatEventListener } from "./useChatStateStore";
import { useContentCapsEpoch } from "./useContentCapsEpoch";
import { useWebSocketConnection } from "./useWebSocketConnection";

const EDITOR_TABS_KEY = "loop-editor-tabs";
// How far " copy N" counts before a paste gives up on finding a free name.
const MAX_COPY_SUFFIX = 100;

interface EditorTabsState {
  tabs: string[];
  selected: string | null;
  // The open preview tab (single click), kept so it opens as a preview again.
  preview?: string | null;
}

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
  // Called when the media element failed to load imageURL: the content link
  // may have expired, so it is minted again once per shown file.
  retryMedia: () => void;
  loading: boolean;
  error: string | null;

  // VCS change markers (added/modified/deleted lines vs git HEAD) for the
  // currently-open file, fed to the editor's gutter.
  gitChanges: GitLineChanges;

  // Dirty + auto-refresh state
  dirtyTabs: Set<string>;
  pendingRefresh: Map<string, string>;
  // Tabs the agent rewrote that the user hasn't looked at since.
  agentEditedTabs: Set<string>;
  // Tabs whose file is gone from disk; they keep their last content and
  // saving one writes the file back.
  deletedTabs: Set<string>;
  autoSaveOnBlur: boolean;
  previewTabsEnabled: boolean;

  // Refs the editor view attaches to
  codeEditorRef: React.RefObject<CodeEditorHandle | null>;

  // Actions on the file tree
  loadDir: (path: string, rootIndex?: number) => Promise<void>;
  refreshTree: () => Promise<void>;
  /** Called by a file tree panel as it mounts, to load the root listings. */
  showTree: () => void;
  toggleDir: (pathKey: string) => void;
  /** Where a tab was left: its selection and scroll. */
  tabViewFor: (pathKey: string) => TabView | undefined;
  /** Records where a tab is, kept per channel across tab and channel switches. */
  saveTabView: (pathKey: string, view: TabView) => void;
  /** Expands a directory and every directory above it, and selects it. */
  revealDir: (pathKey: string) => void;
  /** Opens a file's folders in the tree and asks the tree to scroll to it. */
  revealFile: (pathKey: string) => void;
  /** The last file revealFile showed; seq tells repeated reveals apart. */
  revealRequest: { path: string; seq: number } | null;
  setSelectedDir: (pathKey: string) => void;
  addExtraDir: (dir: string) => Promise<void>;
  handleCreateFile: (name: string) => void;
  handleDeleteFilePath: (pathKey: string) => void;
  handleCreateDirPath: (name: string) => void;
  handleDeleteDirPath: (pathKey: string) => void;
  /** Renames a file or directory to newRelPath in its root; its open tabs
   * follow. Rejects with the reason when the rename fails. */
  handleRenamePath: (pathKey: string, newRelPath: string) => Promise<void>;
  /** Copies a file or directory into the directory destDirKey, in any root.
   * A name already taken there gets " copy", then " copy 2" and so on.
   * Resolves with the copy's key; rejects with the reason when it fails. */
  handleCopyPath: (pathKey: string, isDir: boolean, destDirKey: string) => Promise<string>;

  // Actions on tabs/content
  switchToTab: (pathKey: string) => void;
  openFile: (path: string) => void;
  promoteFile: (path: string) => void;
  closeTab: (path: string, e?: React.MouseEvent) => void;
  /** Closes several tabs at once, the selection moving as closeTab moves it. */
  closeTabs: (paths: readonly string[]) => void;
  /** Makes a clean tab the preview tab, closing the one it takes over from. */
  unpinFile: (path: string) => void;
  markDirty: () => void;
  saveFile: (filePath?: string) => void;
  saveAllDirty: () => void;
  clearError: () => void;
  /** Open a file (a preview tab, or a kept one when pin) and scroll to the given 1-based line once loaded. */
  openFileAtLine: (path: string, line: number | null, pin?: boolean) => void;

  // Auto-refresh resolution
  acceptPendingRefresh: (pathKey: string) => void;
  dismissPendingRefresh: (pathKey: string) => void;
  /** The tab whose unsaved changes are shown against its pending disk version. */
  comparing: string | null;
  /** Show (a tab's path) or hide (null) the comparison; edits while shown keep the disk version pending. */
  setComparing: (pathKey: string | null) => void;
  clearAgentEdited: (pathKey: string) => void;

  /** The channel's editor settings, from its config. */
  editorConfig: EditorConfig;
  /** A tab's text at HEAD, shown under its changes; null when not shown. */
  headCompare: { pathKey: string; content: string } | null;
  /** Shows the active tab's changes since HEAD, or hides them. */
  toggleHeadCompare: () => void;
  /** Something the editor couldn't do for a tab (format on save, the diff against HEAD). */
  notice: { pathKey: string; message: string } | null;
  dismissNotice: () => void;
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
  const [treeShown, setTreeShown] = useState(false);
  const [treeRootIndexes, setTreeRootIndexes] = useState<number[] | null>(null);
  const [selectedDir, setSelectedDir] = useState(makePathKey(0, ""));

  const [openTabs, setOpenTabs] = useState<string[]>(() => loadEditorTabs(channelId, tabsKey).tabs);
  const openTabsRef = useRef(openTabs);
  openTabsRef.current = openTabs;
  const [selectedPath, setSelectedPath] = useState<string | null>(() => loadEditorTabs(channelId, tabsKey).selected);
  const [previewTab, setPreviewTab] = useState<string | null>(() => {
    const saved = loadEditorTabs(channelId, tabsKey);
    return saved.preview && saved.tabs.includes(saved.preview) ? saved.preview : null;
  });
  // Where each open tab was left, restored as it opens again.
  const [tabViews] = useState(() => loadTabViews(channelId));

  const [fileContent, setFileContent] = useState<string | null>(null);
  const [isBinary, setIsBinary] = useState(false);
  const [binarySize, setBinarySize] = useState(0);
  const [imageURL, setImageURL] = useState<string | null>(null);
  // Counter bumped on agent refresh of the active image tab; appended as `?t=`
  // to the URL so the browser re-fetches instead of serving the cached image.
  const imageVersionRef = useRef(0);
  // Content links are minted asynchronously; a newer show/clear wins over a
  // link still being minted for an earlier tab.
  const mediaReqRef = useRef(0);
  // The media file on show, so a fresh content link can be resolved for it.
  const mediaShownRef = useRef<{ rp: string; ri: number; retried: boolean } | null>(null);
  const showMedia = useCallback(
    (rp: string, ri: number) => {
      const req = ++mediaReqRef.current;
      mediaShownRef.current = { rp, ri, retried: false };
      buildFileUrl(channelId, rp, ri, imageVersionRef.current)
        .then((url) => {
          if (mediaReqRef.current === req) setImageURL(url);
        })
        .catch(() => {
          if (mediaReqRef.current === req) setImageURL(null);
        });
    },
    [channelId],
  );
  const clearMedia = useCallback(() => {
    mediaReqRef.current++;
    mediaShownRef.current = null;
    setImageURL(null);
  }, []);
  const retryMedia = useCallback(() => {
    const shown = mediaShownRef.current;
    if (!shown || shown.retried) return;
    forgetContentCap({ kind: "raw", channelId, root: shown.ri });
    showMedia(shown.rp, shown.ri);
    if (mediaShownRef.current) mediaShownRef.current.retried = true;
  }, [channelId, showMedia]);
  // Every content link was forgotten (the daemon restarted): resolve the
  // shown file's again.
  const capsEpoch = useContentCapsEpoch();
  const capsEpochRef = useRef(capsEpoch);
  useEffect(() => {
    if (capsEpochRef.current === capsEpoch) return;
    capsEpochRef.current = capsEpoch;
    const shown = mediaShownRef.current;
    if (shown) showMedia(shown.rp, shown.ri);
  }, [capsEpoch, showMedia]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [gitChanges, setGitChanges] = useState<GitLineChanges>(emptyGitLineChanges);

  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set());
  const [pendingRefresh, setPendingRefresh] = useState<Map<string, string>>(new Map());
  const [comparing, setComparing] = useState<string | null>(null);
  const comparingRef = useRef(comparing);
  comparingRef.current = comparing;
  // A comparison ends with its pending disk version (Replace, Keep, a delete).
  if (comparing !== null && !pendingRefresh.has(comparing)) setComparing(null);
  const [agentEditedTabs, setAgentEditedTabs] = useState<Set<string>>(new Set());
  const [deletedTabs, setDeletedTabs] = useState<Set<string>>(new Set());
  // The last content of a deleted tab that isn't on screen, shown when the
  // user switches back to it.
  const deletedContentRef = useRef(new Map<string, string>());
  const deletedTabsRef = useRef(deletedTabs);
  deletedTabsRef.current = deletedTabs;
  const setDeleted = useCallback((pathKey: string, deleted: boolean) => {
    if (!deleted) deletedContentRef.current.delete(pathKey);
    setDeletedTabs((prev) => {
      if (prev.has(pathKey) === deleted) return prev;
      const next = new Set(prev);
      if (deleted) next.add(pathKey);
      else next.delete(pathKey);
      return next;
    });
  }, []);
  const clearAgentEdited = useCallback((pathKey: string) => {
    setAgentEditedTabs((prev) => {
      if (!prev.has(pathKey)) return prev;
      const next = new Set(prev);
      next.delete(pathKey);
      return next;
    });
  }, []);
  const [editorConfig, setEditorConfig] = useState<EditorConfig>(DEFAULT_EDITOR_CONFIG);
  const autoSaveOnBlur = editorConfig.auto_save_on_blur;
  const previewTabsEnabled = editorConfig.preview_tabs;
  const [headCompare, setHeadCompare] = useState<{ pathKey: string; content: string } | null>(null);
  const [notice, setNotice] = useState<{ pathKey: string; message: string } | null>(null);

  const codeEditorRef = useRef<CodeEditorHandle | null>(null);
  const pendingScrollRef = useRef<{ pathKey: string; line: number } | null>(null);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;
  // Counts switchToTab's loads, so a fetch that finishes after another tab
  // was opened is dropped.
  const loadSeqRef = useRef(0);
  const dirtyTabsRef = useRef(dirtyTabs);
  dirtyTabsRef.current = dirtyTabs;
  const autoSaveOnBlurRef = useRef(autoSaveOnBlur);
  autoSaveOnBlurRef.current = autoSaveOnBlur;
  const expandedDirsRef = useRef(expandedDirs);
  expandedDirsRef.current = expandedDirs;
  const rootsRef = useRef(roots);
  rootsRef.current = roots;
  const dirtyContentRef = useRef(new Map<string, string>());
  // The last content known to be on disk per text tab, so a refresh that
  // reads back the same bytes (our own save, a no-op write) changes nothing.
  const diskContentRef = useRef(new Map<string, string>());

  // The channel's editor config, read again as the window comes back, so an
  // edit to the config file shows.
  const loadEditorConfig = useCallback(() => {
    fetchEditorConfig(channelId).then(setEditorConfig).catch(logErr("loading editor settings"));
  }, [channelId]);
  useEffect(() => {
    loadEditorConfig();
    window.addEventListener("focus", loadEditorConfig);
    return () => window.removeEventListener("focus", loadEditorConfig);
  }, [loadEditorConfig]);

  // The diff against HEAD and a notice belong to their tab: another tab
  // hides them.
  if (headCompare && headCompare.pathKey !== selectedPath) setHeadCompare(null);
  if (notice && notice.pathKey !== selectedPath) setNotice(null);

  const toggleHeadCompare = useCallback(() => {
    const pathKey = selectedPathRef.current;
    if (!pathKey) return;
    if (headCompare?.pathKey === pathKey) {
      setHeadCompare(null);
      return;
    }
    const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
    fetchFileContent(channelId, rp, ri, "HEAD")
      .then((result) => {
        if (selectedPathRef.current !== pathKey) return;
        if (result.binary) {
          setNotice({ pathKey, message: "The file at HEAD is binary." });
          return;
        }
        setNotice(null);
        setHeadCompare({ pathKey, content: result.content });
      })
      .catch((err) => {
        if (selectedPathRef.current !== pathKey) return;
        setNotice({
          pathKey,
          message: err instanceof FileNotFoundError ? "This file isn't in HEAD: it's new, or not in a git repository." : "Couldn't read the file at HEAD.",
        });
      });
  }, [channelId, headCompare]);

  const dismissNotice = useCallback(() => setNotice(null), []);

  // Persist the tab list, the preview tab marked as such.
  useEffect(() => {
    const preview = previewTab && openTabs.includes(previewTab) ? previewTab : null;
    saveEditorTabs(channelId, { tabs: openTabs, selected: selectedPath, preview }, tabsKey);
  }, [channelId, openTabs, selectedPath, previewTab, tabsKey]);

  const loadDir = useCallback(
    async (path: string, rootIndex = 0) => {
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
    },
    [channelId],
  );

  // Load roots on mount.
  useEffect(() => {
    fetchRoots(channelId)
      .then((r) => {
        setRoots(r);
        setTreeRootIndexes(r.map((root) => root.index));
      })
      .catch(() => {
        setTreeRootIndexes([0]);
      });
  }, [channelId]);

  // List each root once a file tree is on screen. Layouts without one skip
  // these requests, one per root, when a chat opens.
  useEffect(() => {
    if (!treeShown || !treeRootIndexes) return;
    for (const index of treeRootIndexes) loadDir(".", index);
  }, [treeShown, treeRootIndexes, loadDir]);

  const showTree = useCallback(() => setTreeShown(true), []);

  // On mount, if a tab was persisted, fetch its content.
  useEffect(() => {
    if (!selectedPath) return;
    const { rootIndex: ri, relativePath: rp } = parsePathKey(selectedPath);
    if (isMediaPath(rp)) {
      showMedia(rp, ri);
      setFileContent(null);
      setIsBinary(false);
      return;
    }
    setLoading(true);
    fetchFileContent(channelId, rp, ri)
      .then((result) => {
        if (result.binary) {
          setIsBinary(true);
          setBinarySize(0);
          setFileContent(null);
        } else {
          diskContentRef.current.set(selectedPath, result.content);
          setFileContent(result.content);
        }
      })
      .catch((err) => {
        if (err instanceof FileNotFoundError) {
          setDeleted(selectedPath, true);
          setFileContent("");
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load file");
      })
      .finally(() => setLoading(false));
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
    if (isMediaPath(rp)) {
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
    setDirtyTabs((prev) => {
      if (prev.has(p)) return prev;
      const next = new Set(prev);
      next.add(p);
      return next;
    });
    setPreviewTab((cur) => (cur === p ? null : cur));
    clearAgentEdited(p);
    // Editing also implicitly dismisses any pending agent refresh for this
    // tab, unless it is being compared: those edits merge the two.
    if (comparingRef.current === p) return;
    setPendingRefresh((prev) => {
      if (!prev.has(p)) return prev;
      const next = new Map(prev);
      next.delete(p);
      return next;
    });
  }, [clearAgentEdited]);

  const saveFile = useCallback(
    (filePath?: string) => {
      const savePath = filePath ?? selectedPathRef.current;
      if (!savePath) return;
      const editor = codeEditorRef.current;
      if (!editor || savePath !== selectedPathRef.current) return;
      editor.appendNewlineIfMissing();
      const content = editor.getContent();
      if (content === null) return;
      const { rootIndex: ri, relativePath: rp } = parsePathKey(savePath);
      // Set before the write lands, so the watcher's echo of it is a no-op.
      diskContentRef.current.set(savePath, content);
      // The daemon formats it when the channel's config turns that on.
      saveFileContent(channelId, rp, content, ri, true)
        .then((saved) => {
          if (saved.content !== undefined) {
            // The formatted text is what's on disk: the editor takes it,
            // unless it was edited again while the save ran.
            diskContentRef.current.set(savePath, saved.content);
            clearAgentEdited(savePath);
            const editor = codeEditorRef.current;
            if (savePath === selectedPathRef.current && editor?.getContent() === content) editor.replaceContent(saved.content);
          }
          if (saved.formatError) setNotice({ pathKey: savePath, message: `Saved without formatting: ${saved.formatError}` });
          else setNotice((cur) => (cur?.pathKey === savePath ? null : cur));
          dirtyContentRef.current.delete(savePath);
          setDeleted(savePath, false);
          setDirtyTabs((prev) => {
            if (!prev.has(savePath)) return prev;
            const next = new Set(prev);
            next.delete(savePath);
            return next;
          });
          // What was saved is on disk now, so a disk version waiting to be
          // resolved (a comparison too) no longer applies.
          setPendingRefresh((prev) => {
            if (!prev.has(savePath)) return prev;
            const next = new Map(prev);
            next.delete(savePath);
            return next;
          });
          if (savePath === selectedPathRef.current) refreshGitChangesRef.current();
        })
        .catch((err) => {
          diskContentRef.current.delete(savePath);
          logErr("saving file")(err);
        });
    },
    [channelId, setDeleted, clearAgentEdited],
  );

  const saveAllDirty = useCallback(() => {
    if (dirtyTabsRef.current.has(selectedPathRef.current ?? "")) saveFile();
  }, [saveFile]);

  const switchToTab = useCallback(
    (pathKey: string) => {
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
      clearAgentEdited(pathKey);
      setError(null);
      setIsBinary(false);
      loadSeqRef.current++;
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      if (isMediaPath(rp)) {
        showMedia(rp, ri);
        setFileContent(null);
        setLoading(false);
        return;
      }
      clearMedia();
      const cached = dirtyContentRef.current.get(pathKey) ?? deletedContentRef.current.get(pathKey);
      if (cached !== undefined) {
        setFileContent(cached);
        return;
      }
      setLoading(true);
      setFileContent(null);
      // Tabs opened in quick succession each fetch; only the last one's
      // content may land, or an earlier, slower fetch shows under its tab.
      const load = ++loadSeqRef.current;
      fetchFileContent(channelId, rp, ri)
        .then((result) => {
          if (load !== loadSeqRef.current) return;
          if (result.binary) {
            setIsBinary(true);
            setBinarySize(0);
            setFileContent(null);
          } else {
            diskContentRef.current.set(pathKey, result.content);
            setFileContent(result.content);
          }
        })
        .catch((err) => {
          if (load !== loadSeqRef.current) return;
          if (err instanceof FileNotFoundError) {
            setDeleted(pathKey, true);
            setFileContent("");
            return;
          }
          setError(err instanceof Error ? err.message : "Failed to load file");
        })
        .finally(() => {
          if (load === loadSeqRef.current) setLoading(false);
        });
    },
    [channelId, saveAllDirty, clearAgentEdited, setDeleted],
  );

  // Single-click open: preview tab if enabled, else permanent.
  const openFile = useCallback(
    (path: string) => {
      if (!previewTabsEnabled) {
        setOpenTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
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
    },
    [previewTab, previewTabsEnabled, openTabs, switchToTab],
  );

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

  // Double-click promote: ensure permanent and active.
  const promoteFile = useCallback(
    (path: string) => {
      setOpenTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
      if (previewTab === path) setPreviewTab(null);
      if (selectedPathRef.current !== path) switchToTab(path);
    },
    [previewTab, switchToTab],
  );

  const openFileAtLine = useCallback(
    (path: string, line: number | null, pin = false) => {
      if (line !== null) {
        pendingScrollRef.current = { pathKey: path, line };
      } else {
        pendingScrollRef.current = null;
      }
      if (pin) promoteFile(path);
      else openFile(path);
      // If it's already the active tab with content loaded, openFile is a no-op
      // and no fileContent effect will fire — scroll synchronously.
      if (line !== null && selectedPathRef.current === path && fileContentRef.current !== null) {
        tryFlushPendingScroll();
      }
    },
    [openFile, promoteFile, tryFlushPendingScroll],
  );

  // Closes tabs, dropping what they hold; the selection moves as closing a
  // single tab moves it.
  const closeTabs = useCallback(
    (paths: readonly string[]) => {
      if (paths.length === 0) return;
      const closing = new Set(paths);
      if (autoSaveOnBlurRef.current && selectedPathRef.current && closing.has(selectedPathRef.current)) saveAllDirty();
      for (const path of paths) {
        dirtyContentRef.current.delete(path);
        diskContentRef.current.delete(path);
        setDeleted(path, false);
        clearAgentEdited(path);
      }
      setDirtyTabs((prev) => {
        if (!paths.some((p) => prev.has(p))) return prev;
        return new Set([...prev].filter((p) => !closing.has(p)));
      });
      setPendingRefresh((prev) => {
        if (!paths.some((p) => prev.has(p))) return prev;
        return new Map([...prev].filter(([p]) => !closing.has(p)));
      });
      if (previewTab && closing.has(previewTab)) setPreviewTab(null);
      const next = selectionAfterClose(openTabsRef.current, selectedPathRef.current, paths);
      setOpenTabs((prev) => prev.filter((p) => !closing.has(p)));
      if (next) switchToTab(next);
      else if (next === null) {
        setSelectedPath(null);
        setFileContent(null);
        setIsBinary(false);
        clearMedia();
        setError(null);
      }
    },
    [previewTab, saveAllDirty, switchToTab, clearAgentEdited, setDeleted],
  );

  const closeTab = useCallback(
    (path: string, e?: React.MouseEvent) => {
      if (e) e.stopPropagation();
      closeTabs([path]);
    },
    [closeTabs],
  );

  // Makes a clean tab the preview tab, so the next file opened replaces it;
  // the preview tab it takes over from closes.
  const unpinFile = useCallback(
    (path: string) => {
      if (!previewTabsEnabled || path === previewTab || !openTabsRef.current.includes(path)) return;
      if (previewTab) closeTabs([previewTab]);
      setPreviewTab(path);
    },
    [previewTab, previewTabsEnabled, closeTabs],
  );

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
      if (isMediaPath(rp)) {
        imageVersionRef.current++;
        showMedia(rp, ri);
        return;
      }
      try {
        const result = await fetchFileContent(channelId, rp, ri);
        if (selectedPathRef.current !== pathKey || result.binary) return;
        diskContentRef.current.set(pathKey, result.content);
        const editor = codeEditorRef.current;
        if (!editor) return;
        const current = editor.getContent();
        if (current !== null && result.content !== current) {
          editor.replaceContent(result.content);
          setDirtyTabs((prev) => {
            if (!prev.has(pathKey)) return prev;
            const next = new Set(prev);
            next.delete(pathKey);
            return next;
          });
        }
      } catch {
        /* file may have been deleted */
      }
    }
  }, [loadDir, channelId]);

  const toggleDir = useCallback(
    (pathKey: string) => {
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
    },
    [dirContents, loadDir],
  );

  const revealDir = useCallback(
    (pathKey: string) => {
      const { rootIndex, relativePath } = parsePathKey(pathKey);
      const parts = relativePath.split("/").filter(Boolean);
      const keys = [makePathKey(rootIndex, ""), ...parts.map((_, i) => makePathKey(rootIndex, parts.slice(0, i + 1).join("/")))];
      for (const key of keys) {
        if (dirContents.has(key)) continue;
        const rel = parsePathKey(key).relativePath;
        loadDir(rel === "" ? "." : rel, rootIndex);
      }
      setExpandedDirs((prev) => {
        if (keys.every((k) => prev.has(k))) return prev;
        return new Set([...prev, ...keys]);
      });
      setSelectedDir(pathKey);
    },
    [dirContents, loadDir],
  );

  // Shows a file in the tree: its folders open and the tree scrolls to it.
  const [revealRequest, setRevealRequest] = useState<{ path: string; seq: number } | null>(null);
  const revealFile = useCallback(
    (pathKey: string) => {
      revealDir(parentDirKey(pathKey));
      setRevealRequest((prev) => ({ path: pathKey, seq: (prev?.seq ?? 0) + 1 }));
    },
    [revealDir],
  );

  const handleCreateFile = useCallback(
    (name: string) => {
      const trimmed = name.trim();
      if (!trimmed) return;
      const { rootIndex } = parsePathKey(selectedDir);
      const { rootIndex: ri, relativePath: rp } = parsePathKey(trimmed);
      const actualRoot = trimmed.includes(":") ? ri : rootIndex;
      const actualPath = trimmed.includes(":") ? rp : trimmed;
      saveFileContent(channelId, actualPath, "", actualRoot)
        .then(() => {
          const parentRelPath = actualPath.includes("/") ? actualPath.substring(0, actualPath.lastIndexOf("/")) : ".";
          loadDir(parentRelPath, actualRoot);
          const newKey = makePathKey(actualRoot, actualPath);
          setOpenTabs((prev) => (prev.includes(newKey) ? prev : [...prev, newKey]));
          switchToTab(newKey);
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Failed to create file");
        });
    },
    [channelId, loadDir, switchToTab, selectedDir],
  );

  const handleDeleteFilePath = useCallback(
    (pathKey: string) => {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      deleteFile(channelId, rp, ri)
        .then(() => {
          setOpenTabs((prev) => {
            const next = prev.filter((p) => p !== pathKey);
            if (pathKey === selectedPathRef.current) {
              if (next.length > 0) {
                switchToTab(next[Math.max(0, Math.min(prev.indexOf(pathKey), next.length - 1))]!);
              } else {
                setSelectedPath(null);
                setFileContent(null);
                setIsBinary(false);
                clearMedia();
                setError(null);
              }
            }
            return next;
          });
          dirtyContentRef.current.delete(pathKey);
          setDirtyTabs((prev) => {
            if (!prev.has(pathKey)) return prev;
            const next = new Set(prev);
            next.delete(pathKey);
            return next;
          });
          const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
          loadDir(parentRelPath, ri);
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Failed to delete file");
        });
    },
    [channelId, loadDir, switchToTab],
  );

  const handleCreateDirPath = useCallback(
    (name: string) => {
      const trimmed = name.trim();
      if (!trimmed) return;
      const { rootIndex } = parsePathKey(selectedDir);
      const { rootIndex: ri, relativePath: rp } = parsePathKey(trimmed);
      const actualRoot = trimmed.includes(":") ? ri : rootIndex;
      const actualPath = trimmed.includes(":") ? rp : trimmed;
      createDir(channelId, actualPath, actualRoot)
        .then(() => {
          const parentRelPath = actualPath.includes("/") ? actualPath.substring(0, actualPath.lastIndexOf("/")) : ".";
          loadDir(parentRelPath, actualRoot);
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Failed to create directory");
        });
    },
    [channelId, loadDir, selectedDir],
  );

  const handleDeleteDirPath = useCallback(
    (pathKey: string) => {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      deleteFile(channelId, rp, ri)
        .then(() => {
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
                clearMedia();
                setError(null);
              }
            }
            return next;
          });
          const parentRelPath = rp.includes("/") ? rp.substring(0, rp.lastIndexOf("/")) : ".";
          loadDir(parentRelPath, ri);
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Failed to delete directory");
        });
    },
    [channelId, loadDir, switchToTab],
  );

  const handleRenamePath = useCallback(
    (pathKey: string, newRelPath: string) => {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      const to = newRelPath.trim().replace(/^\/+|\/+$/g, "");
      if (!to || to === rp) return Promise.resolve();
      const toKey = makePathKey(ri, to);
      const parentOf = (p: string) => (p.includes("/") ? p.substring(0, p.lastIndexOf("/")) : ".");
      return renameFile(channelId, rp, to, ri).then(() => {
        const move = (p: string) => renamedPathKey(p, pathKey, toKey);
        // The editor remounts for the new path from fileContent, so hand
        // it what's on screen, unsaved edits included.
        const cur = selectedPathRef.current;
        if (cur && move(cur) !== cur) {
          const content = codeEditorRef.current?.getContent();
          if (content !== null && content !== undefined) setFileContent(content);
          setSelectedPath(move(cur));
        }
        setOpenTabs((prev) => prev.map(move));
        setPreviewTab((prev) => (prev ? move(prev) : prev));
        renameMapKeys(dirtyContentRef.current, pathKey, toKey);
        renameMapKeys(tabViews, pathKey, toKey);
        renameMapKeys(diskContentRef.current, pathKey, toKey);
        setDirtyTabs((prev) => renameSetKeys(prev, pathKey, toKey));
        setAgentEditedTabs((prev) => renameSetKeys(prev, pathKey, toKey));
        setPendingRefresh((prev) => {
          const next = new Map(prev);
          renameMapKeys(next, pathKey, toKey);
          return next;
        });
        setExpandedDirs((prev) => renameSetKeys(prev, pathKey, toKey));
        setSelectedDir((prev) => move(prev));
        // The watcher may already have marked the old path deleted.
        for (const k of deletedTabsRef.current) {
          if (move(k) !== k) setDeleted(k, false);
        }
        loadDir(parentOf(rp), ri);
        if (parentOf(to) !== parentOf(rp)) loadDir(parentOf(to), ri);
      });
    },
    [channelId, loadDir, setDeleted, tabViews],
  );

  const handleCopyPath = useCallback(
    async (pathKey: string, isDir: boolean, destDirKey: string) => {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      const { rootIndex: di, relativePath: dp } = parsePathKey(destDirKey);
      const name = rp.split("/").pop() ?? rp;
      const at = (n: string) => (dp ? `${dp}/${n}` : n);
      // Beside itself it starts at " copy"; elsewhere it keeps its name first.
      let n = parentDirKey(pathKey) === destDirKey ? 1 : 0;
      for (;;) {
        const to = at(n === 0 ? name : copyName(name, n, isDir));
        try {
          await copyFile(channelId, rp, to, ri, di);
        } catch (err) {
          if (err instanceof CopyTargetExistsError && n < MAX_COPY_SUFFIX) {
            n++;
            continue;
          }
          throw err;
        }
        await loadDir(dp || ".", di);
        revealDir(destDirKey);
        return makePathKey(di, to);
      }
    },
    [channelId, loadDir, revealDir],
  );

  const addExtraDir = useCallback(
    async (dir: string) => {
      const currentExtras = rootsRef.current.filter((r) => r.index > 0).map((r) => r.path);
      await updateExtraDirs(channelId, [...currentExtras, dir]);
      const r = await fetchRoots(channelId);
      setRoots(r);
      for (const root of r) loadDir(".", root.index);
    },
    [channelId, loadDir],
  );

  // Cmd+S — immediate save.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        // Saving a deleted tab writes the file back; auto-save never does.
        if (deletedTabsRef.current.has(selectedPathRef.current ?? "")) saveFile();
        else saveAllDirty();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [saveAllDirty, saveFile]);

  const tabViewFor = useCallback((pathKey: string) => tabViews.get(pathKey), [tabViews]);
  const saveTabView = useCallback(
    (pathKey: string, view: TabView) => {
      tabViews.set(pathKey, view);
      saveTabViews(channelId, tabViews, openTabsRef.current);
    },
    [channelId, tabViews],
  );
  // A closed tab's view goes with it.
  useEffect(() => {
    for (const key of tabViews.keys()) if (!openTabs.includes(key)) tabViews.delete(key);
    saveTabViews(channelId, tabViews, openTabs);
  }, [channelId, tabViews, openTabs]);

  // Re-reads an open tab from disk after something else may have changed
  // it: an agent's Edit/Write, the file watcher, or the window regaining
  // focus. Unchanged bytes are left alone. A tab with unsaved edits keeps
  // them and offers the disk version instead; a clean one is replaced and
  // marked until the user looks at it.
  const refreshFromDisk = useCallback(
    (pathKey: string) => {
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      const markEdited = () =>
        setAgentEditedTabs((prev) => {
          if (prev.has(pathKey)) return prev;
          const next = new Set(prev);
          next.add(pathKey);
          return next;
        });
      if (isMediaPath(rp)) {
        markEdited();
        if (pathKey === selectedPathRef.current) {
          imageVersionRef.current++;
          showMedia(rp, ri);
        }
        return;
      }
      fetchFileContent(channelId, rp, ri)
        .then((result) => {
          if (result.binary || !openTabsRef.current.includes(pathKey)) return;
          setDeleted(pathKey, false);
          if (diskContentRef.current.get(pathKey) === result.content) return;
          diskContentRef.current.set(pathKey, result.content);
          if (dirtyTabsRef.current.has(pathKey)) {
            // The user resolves it via Replace / Keep mine.
            setPendingRefresh((prev) => {
              const next = new Map(prev);
              next.set(pathKey, result.content);
              return next;
            });
            return;
          }
          markEdited();
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
        })
        .catch((err) => {
          if (!(err instanceof FileNotFoundError)) {
            logErr("refreshing file from disk")(err);
            return;
          }
          if (!openTabsRef.current.includes(pathKey)) return;
          // Keep what the tab showed: the editor holds it when on screen,
          // the disk baseline (or the unsaved edits) otherwise.
          const last = diskContentRef.current.get(pathKey);
          diskContentRef.current.delete(pathKey);
          if (pathKey !== selectedPathRef.current && last !== undefined) deletedContentRef.current.set(pathKey, last);
          setPendingRefresh((prev) => {
            if (!prev.has(pathKey)) return prev;
            const next = new Map(prev);
            next.delete(pathKey);
            return next;
          });
          clearAgentEdited(pathKey);
          setDeleted(pathKey, true);
        });
    },
    [channelId, showMedia, setDeleted, clearAgentEdited],
  );
  const refreshFromDiskRef = useRef(refreshFromDisk);
  refreshFromDiskRef.current = refreshFromDisk;

  // Save on blur, re-read from disk on focus (picks up external edits).
  useEffect(() => {
    const onBlur = () => {
      if (autoSaveOnBlurRef.current) saveAllDirty();
    };
    const onFocus = () => {
      const pathKey = selectedPathRef.current;
      if (!pathKey) return;
      const { rootIndex: ri, relativePath: rp } = parsePathKey(pathKey);
      if (isMediaPath(rp)) {
        imageVersionRef.current++;
        showMedia(rp, ri);
        return;
      }
      refreshFromDiskRef.current(pathKey);
      refreshGitChangesRef.current();
    };
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);
    return () => {
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("focus", onFocus);
    };
  }, [saveAllDirty, showMedia]);

  // Watch the open tabs and, with a tree on screen, its expanded dirs, so
  // changes made outside Loop show up as they happen.
  const watchMessage = fileWatchMessage(openTabs, treeShown ? expandedDirs : []);
  const watchMessageRef = useRef(watchMessage);
  watchMessageRef.current = watchMessage;
  const { send: sendWatch } = useWebSocketConnection({
    path: `/api/channels/${encodeURIComponent(channelId)}/files/watch`,
    enabled: channelId !== "",
    onOpen: (ws) => ws.send(watchMessageRef.current),
    onMessage: (event) => {
      const change = parseFileWatchChange(event.data);
      if (!change) return;
      if (change.type === "file") {
        refreshFromDiskRef.current(change.pathKey);
        return;
      }
      if (!expandedDirsRef.current.has(change.pathKey)) return;
      const { rootIndex, relativePath } = parsePathKey(change.pathKey);
      loadDir(relativePath === "" ? "." : relativePath, rootIndex);
    },
  });
  useEffect(() => {
    sendWatch(watchMessage);
  }, [sendWatch, watchMessage]);

  // Auto-refresh on agent Edit/Write tool events. tool.use is
  // broadcast when the model announces the call, before the file is written,
  // so the paths are remembered there and re-read on the call's tool.result.
  useEffect(() => {
    if (!subscribeChatEvents) return;
    const pendingEdits = new Map<string, string>();
    const refreshOpen = (filePath: string) => {
      const pathKey = matchAbsPathToKey(filePath, rootsRef.current);
      if (pathKey && openTabsRef.current.includes(pathKey)) refreshFromDiskRef.current(pathKey);
    };
    const handler = (event: WSEvent) => {
      if (event.type === "tool.use") {
        const data = event.data as ToolUseData;
        const filePath = editToolFilePath(data.tool_name, data.input);
        if (!filePath) return;
        if (data.tool_use_id) pendingEdits.set(data.tool_use_id, filePath);
        else refreshOpen(filePath);
        return;
      }
      if (event.type === "tool.result") {
        const data = event.data as ToolResultData;
        if (!data.tool_use_id) return;
        const filePath = pendingEdits.get(data.tool_use_id);
        if (!filePath) return;
        pendingEdits.delete(data.tool_use_id);
        if (!data.is_error) refreshOpen(filePath);
      }
    };
    return subscribeChatEvents(handler);
  }, [subscribeChatEvents]);

  const acceptPendingRefresh = useCallback(
    (pathKey: string) => {
      const content = pendingRefresh.get(pathKey);
      if (content === undefined) return;
      setPendingRefresh((prev) => {
        if (!prev.has(pathKey)) return prev;
        const next = new Map(prev);
        next.delete(pathKey);
        return next;
      });
      dirtyContentRef.current.delete(pathKey);
      setDirtyTabs((prev) => {
        if (!prev.has(pathKey)) return prev;
        const next = new Set(prev);
        next.delete(pathKey);
        return next;
      });
      if (pathKey === selectedPathRef.current) {
        const editor = codeEditorRef.current;
        if (editor) {
          editor.replaceContent(content);
        } else {
          setFileContent(content);
        }
      }
    },
    [pendingRefresh],
  );

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
    retryMedia,
    loading,
    error,
    gitChanges,
    dirtyTabs,
    pendingRefresh,
    agentEditedTabs,
    deletedTabs,
    autoSaveOnBlur,
    previewTabsEnabled,
    codeEditorRef,
    loadDir,
    refreshTree,
    showTree,
    toggleDir,
    revealDir,
    revealFile,
    revealRequest,
    tabViewFor,
    saveTabView,
    setSelectedDir,
    addExtraDir,
    handleCreateFile,
    handleDeleteFilePath,
    handleCreateDirPath,
    handleDeleteDirPath,
    handleRenamePath,
    handleCopyPath,
    switchToTab,
    openFile,
    openFileAtLine,
    promoteFile,
    closeTab,
    closeTabs,
    unpinFile,
    markDirty,
    saveFile,
    saveAllDirty,
    clearError,
    acceptPendingRefresh,
    dismissPendingRefresh,
    comparing,
    setComparing,
    clearAgentEdited,
    editorConfig,
    headCompare,
    toggleHeadCompare,
    notice,
    dismissNotice,
  };
}

/** Map a tool's absolute file path to a {rootIndex, relativePath} pathKey.
 * Implementation lives in ./editorPaths so it is unit-testable in isolation. */
