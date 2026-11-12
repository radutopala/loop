import { useCallback, useEffect, useRef, useState } from "react";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { HoverTip } from "../chat/HoverTip";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";
import { FileTree, makePathKey, parsePathKey } from "./EditorFileTree";
import { FilePanel } from "./FilePanel";
import { fileSearchLabel, nextSearchIndex, useFileSearch } from "./fileSearch";

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
    setSelectedDir,
    addExtraDir,
    handleCreateFile,
    handleDeleteFilePath,
    handleCreateDirPath,
    handleDeleteDirPath,
    handleRenamePath,
    openFile,
    promoteFile,
  } = editorState;

  const [newFileName, setNewFileName] = useState<string | null>(null);
  const [newDirName, setNewDirName] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; path: string; isDir: boolean } | null>(null);
  const [treeError, setTreeError] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<{ pathKey: string; value: string } | null>(null);
  const [query, setQuery] = useState("");
  const [activeResult, setActiveResult] = useState(0);
  const results = useFileSearch(channelId, query);
  const renameInputRef = useRef<HTMLInputElement>(null);
  const treeRef = useRef<HTMLDivElement>(null);
  const [revealed, setRevealed] = useState<string | null>(null);

  // Scroll a directory opened from the search into view once the tree shows it.
  useEffect(() => {
    if (!revealed || results) return;
    const el = treeRef.current?.querySelector(`[data-testid="file-tree-entry"][data-path="${CSS.escape(revealed)}"]`);
    if (el) {
      el.scrollIntoView({ block: "nearest" });
      setRevealed(null);
    }
  });

  useEffect(() => {
    setActiveResult(0);
  }, [results]);

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
        <div style={{ padding: "2px 8px 4px", flexShrink: 0 }}>
          <input
            data-testid="file-tree-search"
            type="search"
            value={query}
            placeholder="Search files and directories"
            spellCheck={false}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              const next = nextSearchIndex(e.key, activeResult, results?.length ?? 0);
              if (next !== null) {
                e.preventDefault();
                setActiveResult(next);
              } else if (e.key === "Enter") {
                openResult(activeResult, e.metaKey || e.ctrlKey);
              } else if (e.key === "Escape" && query) {
                e.stopPropagation();
                setQuery("");
              }
            }}
            style={{
              width: "100%",
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
          {!results &&
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

const INPUT_STYLE: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  fontSize: 11,
  fontFamily: fonts.mono,
  padding: "2px 4px",
  borderRadius: 3,
  outline: "none",
};
