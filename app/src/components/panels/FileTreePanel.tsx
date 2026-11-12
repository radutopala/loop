import { useCallback, useEffect, useRef, useState } from "react";
import type { RootEntry } from "../../api/loopApi";
import { parentDirKey } from "../../hooks/editorPaths";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { storageGet, storageRemove, storageSet } from "../../utils/storage";
import { HoverTip } from "../chat/HoverTip";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";
import { type ContentSearch, contentHits, contentSummary, highlightParts, MIN_CONTENT_QUERY, useContentSearch } from "./contentSearch";
import { FileTree, makePathKey, parsePathKey } from "./EditorFileTree";
import { FilePanel } from "./FilePanel";
import { fileSearchLabel, nextSearchIndex, useFileSearch } from "./fileSearch";
import { followActiveFileKey, followTooltip, resolveFollow, storedFollow } from "./followActiveFile";

interface FileTreePanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  editorState: EditorStateApi;
  embedded?: boolean;
  maximized?: boolean;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function FileTreePanel({ channelId, dirPath, branch, editorState, embedded, ...panelProps }: FileTreePanelProps) {
  const { colors } = useTheme();
  const {
    roots,
    expandedDirs,
    dirContents,
    selectedDir,
    selectedPath,
    previewTab,
    loadDir,
    refreshTree,
    showTree,
    toggleDir,
    revealDir,
    revealRequest,
    setSelectedDir,
    addExtraDir,
    handleCreateFile,
    handleDeleteFilePath,
    handleCreateDirPath,
    handleDeleteDirPath,
    handleRenamePath,
    openFile,
    openFileAtLine,
    promoteFile,
    editorConfig,
  } = editorState;

  const [newFileName, setNewFileName] = useState<string | null>(null);
  const [newDirName, setNewDirName] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; path: string; isDir: boolean } | null>(null);
  const [treeError, setTreeError] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<{ pathKey: string; value: string } | null>(null);
  const [query, setQuery] = useState("");
  const [activeResult, setActiveResult] = useState(0);
  // Content mode searches inside the files; the name search is off then.
  const [contentMode, setContentMode] = useState(false);
  const [regex, setRegex] = useState(false);
  const [caseSensitive, setCaseSensitive] = useState(false);
  const results = useFileSearch(channelId, contentMode ? "" : query);
  const contentActive = contentMode && query.trim() !== "";
  const content = useContentSearch(channelId, query, { regex, caseSensitive }, contentMode);
  const hits = content.state === "done" ? contentHits(content.result.files) : [];
  // The tree hides while search results show in its place.
  const listing = !!results || contentActive;
  const renameInputRef = useRef<HTMLInputElement>(null);
  const treeRef = useRef<HTMLDivElement>(null);
  const [revealed, setRevealed] = useState<string | null>(null);

  // Scroll a directory opened from the search into view once the tree shows it.
  useEffect(() => {
    if (!revealed || listing) return;
    const el = treeRef.current?.querySelector(`[data-testid="file-tree-entry"][data-path="${CSS.escape(revealed)}"]`);
    if (el) {
      el.scrollIntoView({ block: "nearest" });
      setRevealed(null);
    }
  });

  // A file revealed from its tab: the search closes and the tree scrolls to it.
  const revealSeq = revealRequest?.seq;
  const revealPath = revealRequest?.path;
  useEffect(() => {
    if (!revealPath) return;
    setQuery("");
    setRevealed(revealPath);
  }, [revealSeq, revealPath]);

  // With follow on, the tree follows the active tab. The config sets it;
  // the toggle overrides it for this channel, kept in localStorage.
  const followKey = followActiveFileKey(channelId);
  const [followStored, setFollowStored] = useState(() => ({ key: followKey, value: storageGet(followKey) }));
  const follow = resolveFollow(editorConfig, followStored.key === followKey ? followStored.value : storageGet(followKey));
  const revealDirRef = useRef(revealDir);
  revealDirRef.current = revealDir;
  useEffect(() => {
    if (!follow.on || !selectedPath) return;
    revealDirRef.current(parentDirKey(selectedPath));
    setRevealed(selectedPath);
  }, [follow.on, selectedPath]);
  const toggleFollow = () => {
    const value = storedFollow(editorConfig, !follow.on);
    if (value === null) storageRemove(followKey);
    else storageSet(followKey, value);
    setFollowStored({ key: followKey, value });
  };

  useEffect(() => {
    setActiveResult(0);
  }, [results, content]);

  // Select the name without its extension, as IDEs do, so typing replaces it.
  const renamingKey = renaming?.pathKey;
  useEffect(() => {
    const el = renameInputRef.current;
    if (!renamingKey || !el) return;
    el.focus();
    const v = el.value;
    const start = v.lastIndexOf("/") + 1;
    const dot = v.lastIndexOf(".");
    el.setSelectionRange(start, dot > start ? dot : v.length);
  }, [renamingKey]);

  useEffect(() => {
    showTree();
  }, [showTree]);

  const handleContextMenu = useCallback((e: React.MouseEvent, path: string, isDir: boolean) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, path, isDir });
  }, []);

  const submitNewFile = (name: string) => {
    setNewFileName(null);
    handleCreateFile(name);
  };
  const submitNewDir = (name: string) => {
    setNewDirName(null);
    handleCreateDirPath(name);
  };
  const submitRename = () => {
    if (!renaming) return;
    const { pathKey, value } = renaming;
    setTreeError(null);
    handleRenamePath(pathKey, value)
      .then(() => setRenaming(null))
      .catch((err) => setTreeError(err instanceof Error ? err.message : "Failed to rename"));
  };
  const openResult = (index: number, promote: boolean) => {
    const r = results?.[index];
    if (!r) return;
    const pathKey = makePathKey(r.root_index, r.rel_path);
    if (r.is_dir) {
      // A directory opens in the tree: the search closes onto it.
      revealDir(pathKey);
      setQuery("");
      setRevealed(pathKey);
    } else if (promote) promoteFile(pathKey);
    else openFile(pathKey);
  };
  // A match opens its file at its line; a double-click keeps the tab, as
  // in the tree.
  const openHit = (index: number) => {
    const hit = hits[index];
    if (hit) openFileAtLine(makePathKey(hit.file.root_index, hit.file.rel_path), hit.line);
  };
  const searchCount = contentMode ? hits.length : (results?.length ?? 0);

  const getContextMenuItems = (): MenuItem[] => {
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
    }
    // A root can't be renamed or deleted from here.
    if (ctxRelPath && ctxRelPath !== ".") {
      const { path, isDir } = contextMenu;
      items.push({
        label: "Rename",
        separator: true,
        onClick: () => {
          setTreeError(null);
          setRenaming({ pathKey: path, value: ctxRelPath });
        },
      });
      const name = ctxRelPath.split("/").pop();
      items.push({
        label: "Delete",
        danger: true,
        confirm: isDir ? `Delete ${name} and everything in it?` : `Delete ${name}?`,
        onClick: () => (isDir ? handleDeleteDirPath(path) : handleDeleteFilePath(path)),
      });
    }
    return items;
  };

  const fallbackRoot = roots.length === 0 ? [{ index: 0, path: dirPath, name: dirPath.split("/").pop() || dirPath }] : roots;

  return (
    <FilePanel title="Files" dirPath={dirPath} branch={branch} noPadding embedded={embedded} dataTestId="file-tree-panel" {...panelProps}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%", overflow: "hidden" }}>
        <div style={{ display: "flex", alignItems: "center", padding: "4px 8px 2px", flexShrink: 0 }}>
          <span style={{ flex: 1, fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Workspace</span>
          <HoverTip text={followTooltip(editorConfig, follow)}>
            <button
              onClick={toggleFollow}
              aria-label="Follow active file"
              aria-pressed={follow.on}
              data-follow-source={follow.source}
              data-testid="file-tree-auto-reveal"
              style={{
                background: "none",
                border: "none",
                color: follow.on ? colors.active : colors.textDim,
                cursor: "pointer",
                padding: 0,
                lineHeight: 1,
                display: "flex",
                alignItems: "center",
                marginRight: 4,
              }}
              onMouseEnter={(e) => {
                if (!follow.on) e.currentTarget.style.color = colors.textLight;
              }}
              onMouseLeave={(e) => {
                if (!follow.on) e.currentTarget.style.color = colors.textDim;
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="7" />
                <circle cx="12" cy="12" r="2" />
                <line x1="12" y1="1" x2="12" y2="4" />
                <line x1="12" y1="20" x2="12" y2="23" />
                <line x1="1" y1="12" x2="4" y2="12" />
                <line x1="20" y1="12" x2="23" y2="12" />
              </svg>
            </button>
          </HoverTip>
          <HoverTip text={"Refresh files\nList the workspace again from disk"}>
            <button
              onClick={refreshTree}
              aria-label="Refresh files"
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center" }}
              onMouseEnter={(e) => {
                e.currentTarget.style.color = colors.textLight;
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.color = colors.textDim;
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="23 4 23 10 17 10" />
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
              </svg>
            </button>
          </HoverTip>
          <HoverTip text={"New file\nIn the selected directory; a path with / creates its folders too"}>
            <button
              onClick={() => {
                const { relativePath: selRel } = parsePathKey(selectedDir);
                setNewFileName(selRel ? selRel + "/" : "");
              }}
              aria-label="New file"
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center", marginLeft: 4 }}
              onMouseEnter={(e) => {
                e.currentTarget.style.color = colors.textLight;
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.color = colors.textDim;
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
            </button>
          </HoverTip>
          {window.loopAPI?.showOpenDirectoryDialog && (
            <HoverTip text={"Add directory\nShow another folder in this channel's workspace"}>
              <button
                onClick={async () => {
                  const dir = await window.loopAPI?.showOpenDirectoryDialog?.();
                  if (!dir) return;
                  try {
                    setTreeError(null);
                    await addExtraDir(dir);
                  } catch (err) {
                    setTreeError(err instanceof Error ? err.message : "Failed to add directory");
                  }
                }}
                aria-label="Add directory"
                style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center", marginLeft: 4 }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.color = colors.textLight;
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.color = colors.textDim;
                }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                  <line x1="12" y1="11" x2="12" y2="17" />
                  <line x1="9" y1="14" x2="15" y2="14" />
                </svg>
              </button>
            </HoverTip>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "2px 8px 4px", flexShrink: 0 }}>
          <input
            data-testid="file-tree-search"
            type="search"
            value={query}
            placeholder={contentMode ? "Search in file contents" : "Search files and directories"}
            spellCheck={false}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              const next = nextSearchIndex(e.key, activeResult, searchCount);
              if (next !== null) {
                e.preventDefault();
                setActiveResult(next);
              } else if (e.key === "Enter") {
                if (contentMode) openHit(activeResult);
                else openResult(activeResult, e.metaKey || e.ctrlKey);
              } else if (e.key === "Escape" && query) {
                e.stopPropagation();
                setQuery("");
              }
            }}
            style={{
              flex: 1,
              minWidth: 0,
              boxSizing: "border-box",
              padding: "4px 8px",
              fontSize: 11,
              fontFamily: "inherit",
              color: colors.text,
              backgroundColor: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 5,
              outline: "none",
            }}
          />
          {contentMode && (
            <>
              <SearchToggle
                testId="file-tree-search-regex"
                label="Regular expression"
                tip={regex ? "Regular expression: on\nThe query is a regular expression" : "Regular expression: off\nThe query matches as typed"}
                on={regex}
                onClick={() => setRegex((v) => !v)}
              >
                .*
              </SearchToggle>
              <SearchToggle
                testId="file-tree-search-case"
                label="Match case"
                tip={caseSensitive ? "Match case: on\nUpper and lower case must match" : "Match case: off\nCase is ignored"}
                on={caseSensitive}
                onClick={() => setCaseSensitive((v) => !v)}
              >
                Aa
              </SearchToggle>
            </>
          )}
          <SearchToggle
            testId="file-tree-search-contents"
            label="Search in file contents"
            tip={
              contentMode
                ? "Search in file contents: on\nMatching lines in every root, grouped by file; a click opens the file at the line"
                : "Search in file contents: off\nThe search matches file and directory names; turn on to search inside the files"
            }
            on={contentMode}
            onClick={() => setContentMode((v) => !v)}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="4" y1="6" x2="20" y2="6" />
              <line x1="4" y1="12" x2="14" y2="12" />
              <line x1="4" y1="18" x2="11" y2="18" />
              <circle cx="18" cy="16" r="3" />
              <line x1="20.2" y1="18.2" x2="22" y2="20" />
            </svg>
          </SearchToggle>
        </div>
        {treeError && (
          <div data-testid="file-tree-error" style={{ padding: "2px 8px", color: colors.error, fontSize: 11 }}>
            {treeError}
          </div>
        )}
        {renaming && (
          <div style={{ padding: "2px 8px" }}>
            <input
              ref={renameInputRef}
              data-testid="file-tree-rename"
              value={renaming.value}
              onChange={(e) => setRenaming({ ...renaming, value: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === "Enter") submitRename();
                if (e.key === "Escape") {
                  setRenaming(null);
                  setTreeError(null);
                }
              }}
              style={{ ...INPUT_STYLE, background: colors.bg, border: `1px solid ${colors.active}`, color: colors.textLight }}
            />
          </div>
        )}
        {newFileName !== null && (
          <div style={{ padding: "2px 8px" }}>
            <input
              autoFocus
              placeholder="path/to/file.ext"
              value={newFileName}
              onChange={(e) => setNewFileName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") submitNewFile(newFileName);
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
                if (e.key === "Enter") submitNewDir(newDirName);
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
        <div ref={treeRef} style={{ flex: 1, overflow: "auto", padding: "2px 0" }}>
          {contentActive && <ContentResults content={content} activeHit={activeResult} roots={roots} selectedPath={selectedPath} onHover={setActiveResult} onOpen={openHit} onPromote={promoteFile} />}
          {results && results.length === 0 && <div style={{ padding: "4px 8px", color: colors.textDim, fontSize: 11 }}>Nothing matches</div>}
          {results?.map((r, i) => {
            const pathKey = makePathKey(r.root_index, r.rel_path);
            const { name, where } = fileSearchLabel(r, roots);
            const active = i === activeResult;
            return (
              <button
                key={pathKey}
                data-testid="file-search-result"
                data-path={pathKey}
                aria-selected={active}
                onClick={() => openResult(i, false)}
                onDoubleClick={() => openResult(i, true)}
                onMouseEnter={() => setActiveResult(i)}
                onContextMenu={(e) => handleContextMenu(e, pathKey, !!r.is_dir)}
                style={{
                  display: "flex",
                  alignItems: "baseline",
                  gap: 6,
                  width: "100%",
                  padding: "3px 8px",
                  border: "none",
                  background: pathKey === selectedPath ? colors.selectedBg : active ? colors.hoverBg : "none",
                  color: colors.textLight,
                  cursor: "pointer",
                  fontSize: 12,
                  fontFamily: fonts.mono,
                  textAlign: "left",
                  whiteSpace: "nowrap",
                }}
              >
                <span style={{ flexShrink: 0 }}>{r.is_dir ? `${name}/` : name}</span>
                <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", color: colors.textDim, fontSize: 11 }}>{where}</span>
              </button>
            );
          })}
          {!listing &&
            fallbackRoot.map((root) => {
              const rootKey = makePathKey(root.index, "");
              const isRootExpanded = expandedDirs.has(rootKey);
              const isRootSelected = selectedDir === rootKey;
              return (
                <div key={root.index}>
                  <button
                    data-testid="file-tree-root"
                    data-root={root.index}
                    aria-expanded={isRootExpanded}
                    onClick={() => {
                      setSelectedDir(rootKey);
                      if (isRootExpanded) {
                        toggleDir(rootKey);
                      } else {
                        toggleDir(rootKey);
                        if (!dirContents.has(rootKey)) loadDir(".", root.index);
                      }
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
                    onMouseEnter={(e) => {
                      if (!isRootSelected) e.currentTarget.style.backgroundColor = colors.hoverBg;
                    }}
                    onMouseLeave={(e) => {
                      if (!isRootSelected) e.currentTarget.style.backgroundColor = "transparent";
                    }}
                  >
                    <svg
                      width="10"
                      height="10"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      style={{ flexShrink: 0, opacity: 0.5, transform: isRootExpanded ? "rotate(90deg)" : "rotate(0deg)", transition: "transform 0.15s" }}
                    >
                      <polyline points="9 18 15 12 9 6" />
                    </svg>
                    <svg
                      width="12"
                      height="12"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      style={{ flexShrink: 0, opacity: 0.6 }}
                    >
                      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                    </svg>
                    {root.name}
                  </button>
                  {isRootExpanded && (
                    <FileTree
                      entries={dirContents.get(rootKey) || []}
                      dirContents={dirContents}
                      expandedDirs={expandedDirs}
                      selectedPath={selectedPath}
                      previewTab={previewTab}
                      selectedDir={selectedDir}
                      depth={1}
                      parentPath={rootKey}
                      rootIndex={root.index}
                      onDirClick={(pathKey) => {
                        setSelectedDir(pathKey);
                        toggleDir(pathKey);
                      }}
                      onFileClick={(path) => openFile(path)}
                      onFileDoubleClick={(path) => promoteFile(path)}
                      onContextMenu={handleContextMenu}
                    />
                  )}
                </div>
              );
            })}
        </div>
      </div>
      {contextMenu && <ContextMenu x={contextMenu.x} y={contextMenu.y} items={getContextMenuItems()} onClose={() => setContextMenu(null)} />}
    </FilePanel>
  );
}

interface SearchToggleProps {
  testId: string;
  label: string;
  tip: string;
  on: boolean;
  onClick: () => void;
  children: React.ReactNode;
}

/** A pressed-or-not button beside the search input. */
function SearchToggle({ testId, label, tip, on, onClick, children }: SearchToggleProps) {
  const { colors } = useTheme();
  return (
    <HoverTip text={tip}>
      <button
        type="button"
        data-testid={testId}
        aria-label={label}
        aria-pressed={on}
        onClick={onClick}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minWidth: 20,
          height: 20,
          padding: "0 3px",
          border: `1px solid ${on ? colors.active : "transparent"}`,
          borderRadius: 4,
          background: "none",
          color: on ? colors.active : colors.textDim,
          cursor: "pointer",
          fontSize: 11,
          fontFamily: fonts.mono,
          lineHeight: 1,
          flexShrink: 0,
        }}
      >
        {children}
      </button>
    </HoverTip>
  );
}

interface ContentResultsProps {
  content: ContentSearch;
  activeHit: number;
  roots: RootEntry[];
  selectedPath: string | null;
  onHover: (index: number) => void;
  onOpen: (index: number) => void;
  onPromote: (pathKey: string) => void;
}

/** The content search's matches, grouped by file: the file's name and
 * where it is, then each matching line with the match marked. */
function ContentResults({ content, activeHit, roots, selectedPath, onHover, onOpen, onPromote }: ContentResultsProps) {
  const { colors } = useTheme();
  const note = (text: string, testId?: string, color = colors.textDim) => (
    <div data-testid={testId} style={{ padding: "4px 8px", color, fontSize: 11 }}>
      {text}
    </div>
  );
  if (content.state === "idle") return note(`Type at least ${MIN_CONTENT_QUERY} characters`);
  if (content.state === "searching") return note("Searching…", "content-search-status");
  if (content.state === "error") return note(content.message, "content-search-error", colors.error);
  const { result } = content;
  if (result.files.length === 0) return note("Nothing matches", "content-search-status");
  let index = 0;
  return (
    <>
      {result.files.map((file) => {
        const pathKey = makePathKey(file.root_index, file.rel_path);
        const name = file.rel_path.split("/").pop() ?? file.rel_path;
        const { where } = fileSearchLabel({ ...file, name }, roots);
        return (
          <div key={pathKey} data-testid="content-search-file" data-path={pathKey}>
            <div
              style={{
                display: "flex",
                alignItems: "baseline",
                gap: 6,
                padding: "4px 8px 1px",
                fontSize: 12,
                fontFamily: fonts.mono,
                color: pathKey === selectedPath ? colors.active : colors.textLight,
                whiteSpace: "nowrap",
              }}
            >
              <span style={{ flexShrink: 0, fontWeight: 600 }}>{name}</span>
              <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", color: colors.textDim, fontSize: 11 }}>{where}</span>
              <span style={{ flexShrink: 0, color: colors.textDim, fontSize: 10 }}>{file.matches.length}</span>
            </div>
            {file.matches.map((m) => {
              const i = index++;
              const active = i === activeHit;
              const [before, match, after] = highlightParts(m.text, m.column, m.length);
              return (
                <button
                  key={`${m.line}:${m.column}`}
                  type="button"
                  data-testid="content-search-match"
                  data-line={m.line}
                  aria-selected={active}
                  onClick={() => onOpen(i)}
                  onDoubleClick={() => onPromote(pathKey)}
                  onMouseEnter={() => onHover(i)}
                  style={{
                    display: "flex",
                    alignItems: "baseline",
                    gap: 6,
                    width: "100%",
                    padding: "1px 8px 1px 16px",
                    border: "none",
                    background: active ? colors.hoverBg : "none",
                    color: colors.text,
                    cursor: "pointer",
                    fontSize: 11,
                    fontFamily: fonts.mono,
                    textAlign: "left",
                    whiteSpace: "pre",
                  }}
                >
                  <span style={{ flexShrink: 0, minWidth: 28, textAlign: "right", color: colors.textDim }}>{m.line}</span>
                  <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
                    {before.trimStart()}
                    <mark style={{ background: colors.selectedBg, color: colors.textLight, borderRadius: 2 }}>{match}</mark>
                    {after}
                  </span>
                </button>
              );
            })}
          </div>
        );
      })}
      {note(contentSummary(result), "content-search-summary")}
    </>
  );
}

const INPUT_STYLE: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  fontSize: 11,
  fontFamily: fonts.mono,
  padding: "2px 4px",
  borderRadius: 3,
  outline: "none",
};
