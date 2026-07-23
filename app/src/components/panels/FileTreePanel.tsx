import { useCallback, useState } from "react";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";
import { FileTree, makePathKey, parsePathKey } from "./EditorFileTree";
import { FilePanel } from "./FilePanel";

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

export function FileTreePanel({ channelId: _channelId, dirPath, branch, editorState, embedded, ...panelProps }: FileTreePanelProps) {
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
    toggleDir,
    setSelectedDir,
    addExtraDir,
    handleCreateFile,
    handleDeleteFilePath,
    handleCreateDirPath,
    handleDeleteDirPath,
    openFile,
    promoteFile,
  } = editorState;

  const [newFileName, setNewFileName] = useState<string | null>(null);
  const [newDirName, setNewDirName] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; path: string; isDir: boolean } | null>(null);
  const [addDirError, setAddDirError] = useState<string | null>(null);

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
      items.push({ label: "Delete", danger: true, separator: true, onClick: () => handleDeleteDirPath(contextMenu.path) });
    } else {
      items.push({ label: "Delete", danger: true, separator: true, onClick: () => handleDeleteFilePath(contextMenu.path) });
    }
    return items;
  };

  const fallbackRoot = roots.length === 0 ? [{ index: 0, path: dirPath, name: dirPath.split("/").pop() || dirPath }] : roots;

  return (
    <FilePanel title="Files" dirPath={dirPath} branch={branch} noPadding embedded={embedded} dataTestId="file-tree-panel" {...panelProps}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%", overflow: "hidden" }}>
        <div style={{ display: "flex", alignItems: "center", padding: "4px 8px 2px", flexShrink: 0 }}>
          <span style={{ flex: 1, fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Workspace</span>
          <button
            onClick={refreshTree}
            title="Refresh files"
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
          <button
            onClick={() => {
              const { relativePath: selRel } = parsePathKey(selectedDir);
              setNewFileName(selRel ? selRel + "/" : "");
            }}
            title="New file"
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
          {window.loopAPI?.showOpenDirectoryDialog && (
            <button
              onClick={async () => {
                const dir = await window.loopAPI?.showOpenDirectoryDialog?.();
                if (!dir) return;
                try {
                  setAddDirError(null);
                  await addExtraDir(dir);
                } catch (err) {
                  setAddDirError(err instanceof Error ? err.message : "Failed to add directory");
                }
              }}
              title="Add directory"
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
          )}
        </div>
        {addDirError && <div style={{ padding: "2px 8px", color: colors.error, fontSize: 11 }}>{addDirError}</div>}
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
        <div style={{ flex: 1, overflow: "auto", padding: "2px 0" }}>
          {fallbackRoot.map((root) => {
            const rootKey = makePathKey(root.index, "");
            const isRootExpanded = expandedDirs.has(rootKey);
            const isRootSelected = selectedDir === rootKey;
            return (
              <div key={root.index}>
                <button
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
