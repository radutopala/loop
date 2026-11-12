import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { buildRawFileBase } from "../../api/files";
import { tabDescriptions } from "../../hooks/editorPaths";
import { type BulkClose, tabsToClose } from "../../hooks/tabClose";
import { useContentCapsEpoch } from "../../hooks/useContentCapsEpoch";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { closeWalkthrough, getOpenWalkthrough, goToStep, pauseWalkthrough, playWalkthrough, setEditorSelection, useOpenWalkthrough } from "../../hooks/walkthroughStore";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";
import { openChannelFile } from "../../utils/openChannelFile";
import { HoverTip } from "../chat/HoverTip";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";
import { askAgent, askMessage } from "./askAgent";
import { CodeEditor, isHtmlFile, isMarkdownFile } from "./CodeEditor";
import { FileIcon, parsePathKey } from "./EditorFileTree";
import { loadPreviewModes, type PreviewMode, savePreviewModes } from "./editorPreviewModes";
import type { WalkthroughCard, WalkthroughHandlers, WalkthroughRange } from "./editorWalkthrough";
import { FilePanel } from "./FilePanel";
import { scrollLeftToShow } from "./tabStripScroll";
import { WalkthroughsBadge } from "./WalkthroughsMenu";

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
  /** The pane's leaf id, for the labels in its header. */
  leafId?: string;
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

/** Opens a walkthrough step's file in the channel's editor. */
const openStepFile = (channelId: string, path: string) => openChannelFile(channelId, path);

export function EditorPanel({ dirPath, branch, editorState, embedded, leafId, ...panelProps }: EditorPanelProps) {
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
    deletedTabs,
    clearAgentEdited,
    saveFile,
    pendingRefresh,
    codeEditorRef,
    switchToTab,
    closeTab,
    closeTabs,
    unpinFile,
    revealFile,
    previewTabsEnabled,
    markDirty,
    promoteFile,
    acceptPendingRefresh,
    dismissPendingRefresh,
    comparing,
    setComparing,
    headCompare,
    toggleHeadCompare,
    notice,
    dismissNotice,
  } = editorState;

  // The layout remounts per channel; the modes are saved so they survive it.
  const [previewModes, setPreviewModes] = useState(() => loadPreviewModes(panelProps.channelId));
  const [previewHtml, setPreviewHtml] = useState("");
  const [editorMenu, setEditorMenu] = useState<{ x: number; y: number } | null>(null);
  const [tabMenu, setTabMenu] = useState<{ x: number; y: number; path: string } | null>(null);

  const selected = selectedPath ? parsePathKey(selectedPath) : null;
  const selectedRelPath = selected ? selected.relativePath : null;
  const isMd = selectedRelPath ? isMarkdownFile(selectedRelPath) : false;
  const isHtml = selectedRelPath ? isHtmlFile(selectedRelPath) : false;
  // Markdown opens split, HTML opens rendered; each type remembers its own mode.
  const previewMode = isHtml ? previewModes.html : previewModes.md;
  const setPreviewMode = (mode: PreviewMode) => {
    const next = isHtml ? { ...previewModes, html: mode } : { ...previewModes, md: mode };
    setPreviewModes(next);
    savePreviewModes(panelProps.channelId, next);
  };
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
  const compareWith = comparing === selectedPath ? pendingForActive : undefined;
  // The diff against HEAD gives way to a comparison with the disk version.
  const headDiff = compareWith === undefined && headCompare && headCompare.pathKey === selectedPath ? headCompare.content : undefined;
  const canDiffHead = selectedRelPath !== null && fileContent !== null && !isBinary && compareWith === undefined;

  const absolutePath = (tab: string) => {
    const { rootIndex, relativePath } = parsePathKey(tab);
    return (roots.find((r) => r.index === rootIndex)?.path ?? dirPath) + "/" + relativePath;
  };
  const { channelId } = panelProps;
  const absolutePathRef = useRef(absolutePath);
  absolutePathRef.current = absolutePath;
  // Keep the active tab in the strip's view: on switching tabs, opening
  // one past the edge, and coming back to the channel.
  const tabStripRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const strip = tabStripRef.current;
    if (!strip || !selectedPath) return;
    const show = () => {
      const tab = strip.querySelector<HTMLElement>('[data-testid="editor-tab"][data-active]');
      if (!tab || strip.clientWidth === 0) return;
      const s = strip.getBoundingClientRect();
      const t = tab.getBoundingClientRect();
      const left = scrollLeftToShow({ left: s.left, width: strip.clientWidth, scrollLeft: strip.scrollLeft }, { left: t.left, width: t.width });
      if (left !== null) strip.scrollLeft = left;
    };
    show();
    // The panel and the tabs settle their sizes after mount (layout panes,
    // hidden-then-shown panels), so show the tab again whenever they resize.
    const ro = new ResizeObserver(show);
    ro.observe(strip);
    for (const tab of strip.querySelectorAll('[data-testid="editor-tab"]')) ro.observe(tab);
    return () => ro.disconnect();
  }, [selectedPath, openTabs]);
  const selectedPathRef = useRef(selectedPath);
  selectedPathRef.current = selectedPath;

  // The walkthrough step shows over its file, once that's the file open.
  const walkthrough = useOpenWalkthrough(channelId);
  const walkthroughCard = useMemo((): WalkthroughCard | null => {
    if (!walkthrough || !selectedPath) return null;
    const { rootIndex, relativePath } = parsePathKey(selectedPath);
    if (walkthrough.target.rootIndex !== rootIndex || walkthrough.target.relPath !== relativePath) return null;
    const step = walkthrough.steps[walkthrough.step];
    if (!step) return null;
    return { walkthroughTitle: walkthrough.title, index: walkthrough.step, count: walkthrough.steps.length, step, notice: walkthrough.notice, playing: walkthrough.playing };
  }, [walkthrough, selectedPath]);
  const walkthroughHandlers = useMemo((): WalkthroughHandlers => {
    const go = (delta: number) => {
      pauseWalkthrough(channelId);
      const w = getOpenWalkthrough(channelId);
      if (w) void goToStep(channelId, w.step + delta, openStepFile);
    };
    return {
      prev: () => go(-1),
      next: () => go(1),
      close: () => closeWalkthrough(channelId),
      // Play from the last step starts it over.
      togglePlay: () => {
        const w = getOpenWalkthrough(channelId);
        if (!w) return;
        if (w.playing) pauseWalkthrough(channelId);
        else if (w.step < w.steps.length - 1) playWalkthrough(channelId, openStepFile);
        else void goToStep(channelId, 0, openStepFile).then(() => playWalkthrough(channelId, openStepFile));
      },
      pause: () => pauseWalkthrough(channelId),
      ask: (range) => {
        const w = getOpenWalkthrough(channelId);
        const step = w?.steps[w.step];
        if (!w || !step) return;
        askAgent(channelId, askMessage(step.path, range.start_line, range.end_line, range.text, step.title || w.title)).catch(logErr("asking the agent about a walkthrough step"));
      },
    };
  }, [channelId]);

  // ui_state reports the selection with the file's path, as open_file takes it.
  const handleSelectionChange = useCallback(
    (range: WalkthroughRange | null) => {
      const tab = selectedPathRef.current;
      setEditorSelection(channelId, range && tab ? { path: absolutePathRef.current(tab), ...range } : null);
    },
    [channelId],
  );
  useEffect(() => () => setEditorSelection(channelId, null), [channelId]);
  const handleAskAgent = useCallback(
    (range: WalkthroughRange) => {
      const tab = selectedPathRef.current;
      if (!tab) return;
      askAgent(channelId, askMessage(absolutePathRef.current(tab), range.start_line, range.end_line, range.text)).catch(logErr("asking the agent about the selection"));
    },
    [channelId],
  );

  const tabMenuItems = (): MenuItem[] => {
    if (!tabMenu) return [];
    const tab = tabMenu.path;
    const { relativePath } = parsePathKey(tab);
    const unsaved = (t: string) => dirtyTabs.has(t) || deletedTabs.has(t);
    const items: MenuItem[] = [{ label: "Close", onClick: () => closeTab(tab) }];
    const bulk: [BulkClose, string][] = [
      ["others", "Close others"],
      ["right", "Close to the right"],
      ["saved", "Close saved"],
    ];
    for (const [kind, label] of bulk) {
      const paths = tabsToClose(kind, openTabs, tab, unsaved);
      if (paths.length > 0) items.push({ label, title: "Tabs with unsaved changes stay open", onClick: () => closeTabs(paths) });
    }
    const bulkEnd = items.length;
    if (tab === previewTab) items.push({ label: "Pin tab", separator: true, title: "Keep this tab open when another file opens", onClick: () => promoteFile(tab) });
    else if (previewTabsEnabled && !unsaved(tab))
      items.push({ label: "Unpin tab", separator: true, title: "Make this the preview tab: the next file opened replaces it", onClick: () => unpinFile(tab) });
    items.push({
      label: "Reveal in tree",
      separator: items.length === bulkEnd,
      onClick: () => {
        if (tab !== selectedPath) switchToTab(tab);
        revealFile(tab);
      },
    });
    items.push({ label: "Copy relative path", separator: true, onClick: () => void navigator.clipboard.writeText(relativePath) });
    items.push({ label: "Copy absolute path", onClick: () => void navigator.clipboard.writeText(absolutePath(tab)) });
    return items;
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
              overflow: "hidden",
            }}
          >
            <div ref={tabStripRef} data-testid="editor-tabs" style={{ display: "flex", alignItems: "center", flex: 1, minWidth: 0, overflowX: "auto" }}>
              {openTabs.map((tab) => {
                const isActive = tab === selectedPath;
                const isDirty = dirtyTabs.has(tab);
                const isPreview = tab === previewTab;
                const hasPending = pendingRefresh.has(tab);
                const agentEdited = agentEditedTabs.has(tab);
                const isDeleted = deletedTabs.has(tab);
                const tabRelPath = parsePathKey(tab).relativePath;
                const fileName = tabRelPath.split("/").pop() || tabRelPath;
                const description = descriptions.get(tab);
                const tabAbsPath = absolutePath(tab);
                return (
                  <button
                    key={tab}
                    data-testid="editor-tab"
                    data-path={tabRelPath}
                    data-deleted={isDeleted || undefined}
                    data-active={isActive || undefined}
                    data-preview={isPreview || undefined}
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
                    title={isDeleted ? `${tabAbsPath} — deleted on disk` : hasPending || agentEdited ? `${tabAbsPath} — changed on disk` : tabAbsPath}
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
                    <span
                      style={{
                        fontStyle: isPreview || isDirty ? "italic" : undefined,
                        textDecoration: isDeleted ? "line-through" : undefined,
                        color: isDeleted ? colors.error : undefined,
                      }}
                    >
                      {fileName}
                    </span>
                    {description && <span style={{ color: colors.textDim, opacity: 0.7, fontSize: 10 }}>{description}</span>}
                    {(hasPending || agentEdited) && (
                      <span title="Changed on disk" data-testid="editor-tab-agent-edited" style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: colors.active, display: "block" }} />
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
            {canDiffHead && (
              <HoverTip text={headDiff !== undefined ? "Hide changes since HEAD" : "Changes since HEAD\nShow everything changed in this file since the last commit; revert or keep each change"}>
                <button
                  data-testid="editor-diff-head"
                  aria-pressed={headDiff !== undefined}
                  onClick={toggleHeadCompare}
                  style={{
                    flexShrink: 0,
                    display: "flex",
                    alignItems: "center",
                    padding: "4px 8px",
                    border: "none",
                    background: headDiff !== undefined ? `${colors.active}18` : "none",
                    color: headDiff !== undefined ? colors.active : colors.textDim,
                    cursor: "pointer",
                  }}
                >
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 3v8M8 7h8M8 17h8M5 21h14" />
                  </svg>
                </button>
              </HoverTip>
            )}
          </div>
        )}
        {selectedPath && notice?.pathKey === selectedPath && (
          <div
            data-testid="editor-notice"
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 10px",
              flexShrink: 0,
              backgroundColor: `${colors.warning}18`,
              borderBottom: `1px solid ${colors.border}`,
              fontSize: 11,
              fontFamily: fonts.sans,
              color: colors.textLight,
            }}
          >
            <span title={notice.message} style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "pre", fontFamily: fonts.mono }}>
              {notice.message}
            </span>
            <button
              data-testid="editor-notice-dismiss"
              aria-label="Dismiss"
              onClick={dismissNotice}
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", fontSize: 14, lineHeight: 1, padding: "0 2px" }}
            >
              &times;
            </button>
          </div>
        )}
        {headDiff !== undefined && (
          <div
            data-testid="head-diff-banner"
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
              Changes since HEAD: the committed lines show in red above yours. Revert or keep each change.
            </span>
            <button
              data-testid="head-diff-hide"
              onClick={toggleHeadCompare}
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
              Hide diff
            </button>
          </div>
        )}
        {selectedPath && deletedTabs.has(selectedPath) && (
          <div
            data-testid="editor-deleted-banner"
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 10px",
              flexShrink: 0,
              backgroundColor: `${colors.error}18`,
              borderBottom: `1px solid ${colors.border}`,
              fontSize: 11,
              fontFamily: fonts.sans,
              color: colors.textLight,
            }}
          >
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>This file was deleted on disk.</span>
            <button
              onClick={() => saveFile(selectedPath)}
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
              Restore
            </button>
            <button
              onClick={() => closeTab(selectedPath)}
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
              Close
            </button>
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
            <span data-testid="disk-change-banner" style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {compareWith !== undefined
                ? "The disk version shows in red above your lines. Use disk or keep mine per change, then Keep this."
                : "This file changed on disk. Replace your unsaved changes?"}
            </span>
            <button
              data-testid="disk-change-compare"
              onClick={() => setComparing(compareWith !== undefined ? null : selectedPath)}
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
              {compareWith !== undefined ? "Hide diff" : "Compare"}
            </button>
            <button
              data-testid="disk-change-replace"
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
              data-testid="disk-change-keep"
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
              {compareWith !== undefined ? "Keep this" : "Keep mine"}
            </button>
          </div>
        )}
        {/* The preview-mode toggle floats over the top right of the file,
            out of the tab bar, so many open tabs don't squeeze it. */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, position: "relative" }}>
          {(isMd || isHtml) && (
            <div
              data-testid="preview-mode-toggle"
              style={{
                position: "absolute",
                top: 6,
                right: 16,
                zIndex: 2,
                display: "flex",
                border: `1px solid ${colors.border}`,
                borderRadius: 4,
                overflow: "hidden",
                backgroundColor: colors.sidebar,
                boxShadow: "0 1px 4px rgba(0,0,0,0.25)",
              }}
            >
              {(["editor", "both", "preview"] as const).map((mode) => (
                <button
                  key={mode}
                  data-testid={`preview-mode-${mode}`}
                  aria-pressed={previewMode === mode}
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
            walkthrough={walkthroughCard}
            walkthroughHandlers={walkthroughHandlers}
            onSelectionChange={handleSelectionChange}
            onAskAgent={handleAskAgent}
            tabViewFor={editorState.tabViewFor}
            onTabViewChange={editorState.saveTabView}
            compareWith={compareWith ?? headDiff ?? null}
            compareSource={compareWith !== undefined ? "disk" : "head"}
            tabSize={editorState.editorConfig.tab_size}
            insertSpaces={editorState.editorConfig.insert_spaces}
          />
        </div>
      </div>
      {leafId && <WalkthroughsBadge leafId={leafId} channelId={channelId} open={walkthrough} />}
      {tabMenu && <ContextMenu x={tabMenu.x} y={tabMenu.y} items={tabMenuItems()} onClose={() => setTabMenu(null)} />}
    </FilePanel>
  );
}
