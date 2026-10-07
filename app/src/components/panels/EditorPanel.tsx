import { useCallback, useEffect, useState } from "react";
import { buildRawFileBase } from "../../api/files";
import { tabDescriptions } from "../../hooks/editorPaths";
import { useContentCapsEpoch } from "../../hooks/useContentCapsEpoch";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ContextMenu } from "../shared/ContextMenu";
import { CodeEditor, isHtmlFile, isMarkdownFile } from "./CodeEditor";
import { FileIcon, parsePathKey } from "./EditorFileTree";
import { FilePanel } from "./FilePanel";

// useHtmlBaseURL resolves the <base href> of an HTML preview: a content link
// to the file's directory, minted asynchronously. It resolves again whenever
// the previewed content changes or every link is forgotten, so a preview
// rendered after its link expired (or the daemon restarted) gets a live one;
// the cached link comes back unchanged while it's still valid.
function useHtmlBaseURL(file: { channelId: string; path: string; root: number } | null, content: string): string | null {
  const [url, setUrl] = useState<string | null>(null);
  const capsEpoch = useContentCapsEpoch();
  const channelId = file?.channelId;
  const path = file?.path;
  const root = file?.root;
  // Another file starts without a base; the same file keeps its current one
  // while a fresh one resolves.
  useEffect(() => {
    setUrl(null);
  }, [channelId, path, root]);
  useEffect(() => {
    if (channelId === undefined || path === undefined) return;
    let cancelled = false;
    buildRawFileBase(channelId, path, root)
      .then((u) => {
        if (!cancelled) setUrl(u);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [channelId, path, root, content, capsEpoch]);
  return url;
}

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

type PreviewMode = "editor" | "both" | "preview";

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
    imageURL,
    loading,
    error,
    gitChanges,
    dirtyTabs,
    agentEditedTabs,
    clearAgentEdited,
    pendingRefresh,
    codeEditorRef,
    switchToTab,
    closeTab,
    markDirty,
    promoteFile,
    acceptPendingRefresh,
    dismissPendingRefresh,
  } = editorState;

  const [mdMode, setMdMode] = useState<PreviewMode>("both");
  const [htmlMode, setHtmlMode] = useState<PreviewMode>("preview");
  const [previewHtml, setPreviewHtml] = useState("");
  const [editorMenu, setEditorMenu] = useState<{ x: number; y: number } | null>(null);
  const [tabMenu, setTabMenu] = useState<{ x: number; y: number; path: string } | null>(null);

  const selected = selectedPath ? parsePathKey(selectedPath) : null;
  const selectedRelPath = selected ? selected.relativePath : null;
  const isMd = selectedRelPath ? isMarkdownFile(selectedRelPath) : false;
  const isHtml = selectedRelPath ? isHtmlFile(selectedRelPath) : false;
  // Markdown opens split, HTML opens rendered; each type remembers its own mode.
  const previewMode = isHtml ? htmlMode : mdMode;
  const setPreviewMode = isHtml ? setHtmlMode : setMdMode;
  const htmlBaseURL = useHtmlBaseURL(isHtml && selected ? { channelId: panelProps.channelId, path: selected.relativePath, root: selected.rootIndex } : null, previewHtml);
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

  const absolutePath = (tab: string) => {
    const { rootIndex, relativePath } = parsePathKey(tab);
    return (roots.find((r) => r.index === rootIndex)?.path ?? dirPath) + "/" + relativePath;
  };

  const tabMenuItems = () => {
    if (!tabMenu) return [];
    const { relativePath } = parsePathKey(tabMenu.path);
    return [
      { label: "Copy relative path", onClick: () => void navigator.clipboard.writeText(relativePath) },
      { label: "Copy absolute path", onClick: () => void navigator.clipboard.writeText(absolutePath(tabMenu.path)) },
    ];
  };

  const descriptions = tabDescriptions(
    openTabs.map((tab) => {
      const { rootIndex, relativePath } = parsePathKey(tab);
      return { key: tab, rootName: roots.find((r) => r.index === rootIndex)?.name ?? "", relativePath };
    }),
    hasMultipleRoots,
  );

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
                const agentEdited = agentEditedTabs.has(tab);
                const tabRelPath = parsePathKey(tab).relativePath;
                const fileName = tabRelPath.split("/").pop() || tabRelPath;
                const description = descriptions.get(tab);
                const tabAbsPath = absolutePath(tab);
                return (
                  <button
                    key={tab}
                    data-testid="editor-tab"
                    data-path={tabRelPath}
                    onClick={() => {
                      if (isActive) clearAgentEdited(tab);
                      else switchToTab(tab);
                    }}
                    onDoubleClick={() => {
                      if (isPreview) promoteFile(tab);
                    }}
                    onContextMenu={(e) => {
                      e.preventDefault();
                      setTabMenu({ x: e.clientX, y: e.clientY, path: tab });
                    }}
                    title={hasPending ? `${tabAbsPath} — agent edited externally` : agentEdited ? `${tabAbsPath} — edited by the agent` : tabAbsPath}
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
                    onMouseEnter={(e) => {
                      if (!isActive) e.currentTarget.style.backgroundColor = colors.hoverBg;
                    }}
                    onMouseLeave={(e) => {
                      if (!isActive) e.currentTarget.style.backgroundColor = "transparent";
                    }}
                  >
                    <FileIcon name={fileName} />
                    <span style={{ fontStyle: isPreview || isDirty ? "italic" : undefined }}>{fileName}</span>
                    {description && <span style={{ color: colors.textDim, opacity: 0.7, fontSize: 10 }}>{description}</span>}
                    {(hasPending || agentEdited) && (
                      <span
                        title="Agent modified this file"
                        data-testid="editor-tab-agent-edited"
                        style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.active, display: "block" }}
                      />
                    )}
                    <span onClick={(e) => closeTab(tab, e)} style={{ marginLeft: 2, width: 8, height: 8, display: "flex", alignItems: "center", justifyContent: "center" }}>
                      {isDirty ? (
                        <span style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.warning, display: "block" }} />
                      ) : (
                        <span
                          style={{ opacity: 0.5, fontSize: 14, lineHeight: 1 }}
                          onMouseEnter={(e) => {
                            e.currentTarget.style.opacity = "1";
                          }}
                          onMouseLeave={(e) => {
                            e.currentTarget.style.opacity = "0.5";
                          }}
                        >
                          &times;
                        </span>
                      )}
                    </span>
                  </button>
                );
              })}
            </div>
            {(isMd || isHtml) && (
              <div style={{ display: "flex", flexShrink: 0, margin: "0 6px", border: `1px solid ${colors.border}`, borderRadius: 4, overflow: "hidden" }}>
                {(["editor", "both", "preview"] as const).map((mode) => (
                  <button
                    key={mode}
                    data-testid={`preview-mode-${mode}`}
                    onClick={() => setPreviewMode(mode)}
                    title={mode === "editor" ? (isHtml ? "Source only" : "Editor only") : mode === "both" ? (isHtml ? "Source + Preview" : "Editor + Preview") : "Preview only"}
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
                    {mode === "editor" ? (isHtml ? "Source" : "Edit") : mode === "both" ? "Split" : "Preview"}
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
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>The agent modified this file. Replace your unsaved changes?</span>
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
          htmlBaseURL={htmlBaseURL}
          imageURL={imageURL}
          onMediaError={editorState.retryMedia}
          gitChanges={gitChanges}
        />
      </div>
      {tabMenu && <ContextMenu x={tabMenu.x} y={tabMenu.y} items={tabMenuItems()} onClose={() => setTabMenu(null)} />}
    </FilePanel>
  );
}
