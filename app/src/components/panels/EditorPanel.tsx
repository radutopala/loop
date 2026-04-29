import { useCallback, useState } from "react";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { FilePanel } from "./FilePanel";
import { FileIcon, parsePathKey } from "./EditorFileTree";
import { CodeEditor, isMarkdownFile } from "./CodeEditor";
import type { EditorStateApi } from "../../hooks/useEditorState";

interface EditorPanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  editorState: EditorStateApi;
  maximized?: boolean;
  sidebarOpen?: boolean;
  embedded?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function EditorPanel({ dirPath, branch, editorState, embedded, ...panelProps }: EditorPanelProps) {
  const { colors } = useTheme();
  const {
    roots,
    openTabs,
    selectedPath,
    previewTab,
    fileContent,
    isBinary,
    binarySize,
    loading,
    error,
    dirtyTabs,
    pendingRefresh,
    codeEditorRef,
    switchToTab,
    closeTab,
    markDirty,
    promoteFile,
    acceptPendingRefresh,
    dismissPendingRefresh,
  } = editorState;

  const [previewMode, setPreviewMode] = useState<"editor" | "both" | "preview">("both");
  const [previewHtml, setPreviewHtml] = useState("");
  const [editorMenu, setEditorMenu] = useState<{ x: number; y: number } | null>(null);

  const selectedRelPath = selectedPath ? parsePathKey(selectedPath).relativePath : null;
  const isMd = selectedRelPath ? isMarkdownFile(selectedRelPath) : false;
  const hasMultipleRoots = roots.length > 1;

  const handlePreviewUpdate = useCallback((html: string) => {
    setPreviewHtml(html);
  }, []);

  const handleEditorContextMenu = useCallback((e: React.MouseEvent) => {
    const target = e.target as HTMLElement;
    if (!target.closest(".cm-editor")) return;
    e.preventDefault();
    setEditorMenu({ x: e.clientX, y: e.clientY });
  }, []);

  const pendingForActive = selectedPath ? pendingRefresh.get(selectedPath) : undefined;

  return (
    <FilePanel title="Editor" dirPath={dirPath} branch={branch} noPadding embedded={embedded} dataTestId="editor-panel" {...panelProps}>
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
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
                const hasPending = pendingRefresh.has(tab);
                const { rootIndex: tabRoot, relativePath: tabRelPath } = parsePathKey(tab);
                const fileName = tabRelPath.split("/").pop() || tabRelPath;
                const tabRootEntry = roots.find((r) => r.index === tabRoot);
                const tabLabel = hasMultipleRoots && tabRootEntry ? `${tabRootEntry.name}/${fileName}` : fileName;
                return (
                  <button
                    key={tab}
                    onClick={() => { if (!isActive) switchToTab(tab); }}
                    onDoubleClick={() => { if (isPreview) promoteFile(tab); }}
                    title={hasPending ? `${tabRelPath} — agent edited externally` : tabRelPath}
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
                    {hasPending && (
                      <span title="Agent modified this file" style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.active, display: "block" }} />
                    )}
                    <span
                      onClick={(e) => closeTab(tab, e)}
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
        {pendingForActive !== undefined && selectedPath && (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 10px",
              flexShrink: 0,
              backgroundColor: `${colors.active}18`,
              borderBottom: `1px solid ${colors.border}`,
              fontSize: 11,
              fontFamily: fonts.sans,
              color: colors.textLight,
            }}
          >
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              The agent modified this file. Replace your unsaved changes?
            </span>
            <button
              onClick={() => acceptPendingRefresh(selectedPath)}
              style={{
                background: colors.active,
                border: "none",
                color: colors.textLight,
                cursor: "pointer",
                padding: "2px 8px",
                fontSize: 10,
                fontFamily: fonts.sans,
                borderRadius: 4,
                lineHeight: 1.4,
              }}
            >
              Replace
            </button>
            <button
              onClick={() => dismissPendingRefresh(selectedPath)}
              style={{
                background: "none",
                border: `1px solid ${colors.border}`,
                color: colors.textDim,
                cursor: "pointer",
                padding: "2px 8px",
                fontSize: 10,
                fontFamily: fonts.sans,
                borderRadius: 4,
                lineHeight: 1.4,
              }}
            >
              Keep mine
            </button>
          </div>
        )}
        <CodeEditor
          ref={codeEditorRef}
          fileContent={fileContent}
          isBinary={isBinary}
          binarySize={binarySize}
          selectedRelPath={selectedRelPath}
          selectedPath={selectedPath}
          loading={loading}
          error={error}
          previewMode={previewMode}
          onDocChanged={markDirty}
          onPreviewUpdate={handlePreviewUpdate}
          editorMenu={editorMenu}
          onEditorMenuClose={() => setEditorMenu(null)}
          onEditorContextMenu={handleEditorContextMenu}
          previewHtml={previewHtml}
        />
      </div>
    </FilePanel>
  );
}
