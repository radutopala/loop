import { useCallback, useEffect, useRef, useState } from "react";
import type { DiffResponse, PRInfo } from "../../api/loopApi";
import { fetchDiff, fetchBranches, fetchCommits, fetchPR } from "../../api/loopApi";
import { fetchRoots, type RootEntry } from "../../api/files";
import { useEventStream } from "../../hooks/useEventStream";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { ContextMenu } from "../shared/ContextMenu";
import { DiffViewer, fileKey, parseUnifiedDiff } from "./DiffViewer";
import type { ParsedFile } from "./DiffViewer";
import type { FileLinkOpenDetail } from "../chat/FileLink";
import { CommitHistory } from "./CommitHistory";
import { WorktreesPanel } from "./WorktreesPanel";
import { BranchesPanel } from "./BranchesPanel";
import { storageGet, storageSet } from "../../utils/storage";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.6;
const POLL_INTERVAL = 5_000;
const WIDTH_STORAGE_KEY = "loop-diff-panel-width";

function loadWidth(): number {
  const stored = storageGet(WIDTH_STORAGE_KEY);
  if (stored) {
    const w = parseInt(stored, 10);
    if (w >= MIN_WIDTH) return w;
  }
  // Default to max width on first open
  return Math.floor(window.innerWidth * MAX_WIDTH_PERCENT);
}

function saveWidth(w: number) {
  storageSet(WIDTH_STORAGE_KEY, String(w));
}

interface GitPanelProps {
  channelId: string | null;
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  tabBar?: React.ReactNode;
  embedded?: boolean;
  isWorktree?: boolean;
  hasBranch?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
  onStatusChange?: () => void;
  onClose: () => void;
}

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

export function GitPanel({ channelId, dirPath, branch, maximized, sidebarOpen, tabBar, embedded, isWorktree, hasBranch, onToggleSidebar, onOpenPalette, onToggleMaximize, onImportWorktree, onSelectThread, onStatusChange, onClose }: GitPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const [data, setData] = useState<DiffResponse | null>(null);
  const [parsedFiles, setParsedFiles] = useState<ParsedFile[]>([]);
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [fileContextMenu, setFileContextMenu] = useState<{ x: number; y: number; path: string } | null>(null);
  // Mode state
  type GitMode = "uncommitted" | "branches" | "commits" | "worktrees" | "branchlist";
  const [gitMode, setGitMode] = useState<GitMode>("uncommitted");
  const [branches, setBranches] = useState<string[]>([]);
  const [sourceBranch, setSourceBranch] = useState<string>("");
  const [targetBranch, setTargetBranch] = useState<string>("");
  const [commitBranch, setCommitBranch] = useState<string>("");
  const [commits, setCommits] = useState<import("../../api/loopApi").CommitEntry[]>([]);
  const [commitsLoading, setCommitsLoading] = useState(false);
  const [commitsHasMore, setCommitsHasMore] = useState(true);
  const COMMITS_PAGE = 50;
  const prevDiffRef = useRef<string>("");
  const [diffVersion, setDiffVersion] = useState(0);
  const panelRef = useRef<HTMLDivElement>(null);
  const [pr, setPR] = useState<PRInfo | null>(null);
  // Multi-root workspace support: primary dir_path + extra_dirs. When the
  // channel has no extras, roots.length is 1 and the dropdown is hidden.
  const [roots, setRoots] = useState<RootEntry[]>([]);
  const [rootIndex, setRootIndex] = useState(0);
  // Track which channel's PR has seeded sourceBranch so a manual re-pick by
  // the user isn't clobbered when the same channel's PR refreshes.
  const prSeededRef = useRef<string>("");

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  // Fetch PR info on mount and when channel changes. When a PR is open, seed
  // sourceBranch to the PR's base (only once per channel) so the Branches Diff
  // mode defaults to the PR's target rather than main.
  useEffect(() => {
    if (!channelId) {
      setPR(null);
      return;
    }
    let cancelled = false;
    fetchPR(channelId).then((res) => {
      if (cancelled) return;
      if (res.present && res.pr) {
        setPR(res.pr);
        if (prSeededRef.current !== channelId) {
          setSourceBranch(res.pr.base_ref);
          prSeededRef.current = channelId;
        }
      } else {
        setPR(null);
      }
    }).catch(() => { if (!cancelled) setPR(null); });
    return () => { cancelled = true; };
  }, [channelId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Fetch the channel's roots (primary dir_path + extra_dirs) so the diff can
  // be scoped to any of them via a dropdown. Reset selection when switching
  // channels so we don't carry over an index that's out of range for the next.
  useEffect(() => {
    setRootIndex(0);
    if (!channelId) {
      setRoots([]);
      return;
    }
    let cancelled = false;
    fetchRoots(channelId)
      .then((r) => { if (!cancelled) setRoots(r); })
      .catch(() => { if (!cancelled) setRoots([]); });
    return () => { cancelled = true; };
  }, [channelId]);

  // Fetch branch list when switching to branch or commits mode. Re-runs when
  // `pr` or `rootIndex` changes so the dropdown reflects the active workspace
  // root's branches and worktrees, and so the PR's base ref is included as a
  // selectable option even if it isn't a local branch.
  useEffect(() => {
    if ((gitMode === "branches" || gitMode === "commits") && channelId) {
      fetchBranches(channelId, rootIndex).then((info) => {
        // Combine regular branches + current branch (which may be filtered out of the list).
        const all = new Set(info.branches);
        if (info.current) all.add(info.current);
        // Also include worktree branches.
        for (const wt of info.worktrees ?? []) {
          if (wt.branch) all.add(wt.branch);
        }
        // The PR's base ref only applies to the primary root; extra_dirs may
        // be unrelated repos so don't pollute their dropdown with it.
        if (rootIndex === 0 && pr?.base_ref) all.add(pr.base_ref);
        const sorted = [...all].sort();
        setBranches(sorted);
        // Reset any selection that doesn't exist in the new root's branch
        // set — otherwise the dropdown points at a branch git can't resolve
        // and the commits / diff view comes back empty.
        const defaultSource = (() => {
          if (rootIndex === 0 && pr?.base_ref && sorted.includes(pr.base_ref)) return pr.base_ref;
          const main = sorted.find((b) => b === "main" || b === "master");
          return main ?? info.current ?? sorted[0] ?? "";
        })();
        setSourceBranch((prev) => (prev && all.has(prev) ? prev : defaultSource));
        setTargetBranch((prev) => (prev && all.has(prev) ? prev : info.current ?? ""));
        setCommitBranch((prev) => (prev && all.has(prev) ? prev : info.current ?? ""));
      }).catch(() => {});
    }
  }, [gitMode, channelId, pr?.base_ref, rootIndex]); // eslint-disable-line react-hooks/exhaustive-deps

  // Load first page of commits when branch changes. Re-runs on rootIndex so
  // switching workspace roots refetches commits for the new dir.
  useEffect(() => {
    if (gitMode !== "commits" || !channelId || !commitBranch) return;
    setCommitsLoading(true);
    setCommitsHasMore(true);
    fetchCommits(channelId, commitBranch, COMMITS_PAGE, 0, rootIndex)
      .then((c) => { setCommits(c); setCommitsHasMore(c.length >= COMMITS_PAGE); })
      .catch(() => { setCommits([]); setCommitsHasMore(false); })
      .finally(() => setCommitsLoading(false));
  }, [gitMode, channelId, commitBranch, rootIndex]); // eslint-disable-line react-hooks/exhaustive-deps

  const loadMoreCommits = useCallback(() => {
    if (!channelId || !commitBranch || commitsLoading || !commitsHasMore) return;
    setCommitsLoading(true);
    fetchCommits(channelId, commitBranch, COMMITS_PAGE, commits.length, rootIndex)
      .then((c) => { setCommits((prev) => [...prev, ...c]); setCommitsHasMore(c.length >= COMMITS_PAGE); })
      .catch(() => setCommitsHasMore(false))
      .finally(() => setCommitsLoading(false));
  }, [channelId, commitBranch, commitsLoading, commitsHasMore, commits.length, rootIndex]);

  const load = useCallback(async () => {
    if (!channelId) return;
    if (gitMode === "commits") {
      if (!commitBranch) return;
      setCommitsLoading(true);
      setCommitsHasMore(true);
      try {
        const c = await fetchCommits(channelId, commitBranch, COMMITS_PAGE, 0, rootIndex);
        setCommits(c);
        setCommitsHasMore(c.length >= COMMITS_PAGE);
      } catch {
        setCommits([]);
        setCommitsHasMore(false);
      } finally {
        setCommitsLoading(false);
      }
      return;
    }
    // In branch mode, both branches must be selected
    if (gitMode === "branches" && (!sourceBranch || !targetBranch)) return;
    try {
      const d = gitMode === "branches"
        ? await fetchDiff(channelId, sourceBranch, targetBranch, rootIndex)
        : await fetchDiff(channelId, undefined, undefined, rootIndex);
      // If diff changed, bump version to reset DiffViewer internal state
      if (d.diff !== prevDiffRef.current) {
        prevDiffRef.current = d.diff;
        setDiffVersion((v) => v + 1);
      }
      setData(d);
      if (gitMode === "branches") {
        // Branch-to-branch diff has no staged/unstaged distinction.
        setParsedFiles(parseUnifiedDiff(d.diff));
      } else {
        // Uncommitted mode: parse each section with its known status so a
        // partially-staged file produces two distinct ParsedFile entries.
        setParsedFiles([
          ...parseUnifiedDiff(d.staged_diff ?? "", "staged"),
          ...parseUnifiedDiff(d.unstaged_diff ?? "", "unstaged"),
          ...parseUnifiedDiff(d.conflict_diff ?? "", "conflict"),
          ...parseUnifiedDiff(d.untracked_diff ?? "", "untracked"),
        ]);
      }
    } catch {
      /* ignore fetch errors — will retry on next poll */
    } finally {
      setLoading(false);
    }
  }, [channelId, gitMode, sourceBranch, targetBranch, commitBranch, rootIndex]);

  // Initial load + background polling fallback
  useEffect(() => {
    setLoading(true);
    setData(null);
    setParsedFiles([]);
    setExpandedFiles(new Set());
    load();
    const id = setInterval(load, POLL_INTERVAL);
    return () => clearInterval(id);
  }, [load]);

  // Real-time refresh: debounce-reload on any agent event for this channel.
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onEvent = useCallback(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(load, 1_000);
  }, [load]);
  useEffect(() => () => { if (debounceRef.current) clearTimeout(debounceRef.current); }, []);
  useEventStream({ channelId, onEvent });

  const toggleFile = useCallback((key: string) => {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);

  const expandAll = useCallback(() => {
    if (data) {
      setExpandedFiles(new Set(data.files.map(fileKey)));
    }
  }, [data]);

  const collapseAll = useCallback(() => {
    setExpandedFiles(new Set());
  }, []);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);
      const startX = e.clientX;
      const startWidth = width;

      let lastWidth = startWidth;
      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(maxWidth, Math.max(MIN_WIDTH, startWidth - (ev.clientX - startX)));
        lastWidth = newWidth;
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        saveWidth(lastWidth);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  const handleFileContextMenu = useCallback((e: React.MouseEvent, path: string) => {
    setFileContextMenu({ x: e.clientX, y: e.clientY, path });
  }, []);

  const totalFiles = data?.files.length ?? 0;
  const totalAdd = data?.total_additions ?? 0;
  const totalDel = data?.total_deletions ?? 0;

  const selectStyle: React.CSSProperties = {
    background: colors.surface,
    color: colors.textLight,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    fontSize: 11,
    fontFamily: fonts.mono,
    padding: "1px 4px",
    outline: "none",
    maxWidth: 120,
    cursor: "pointer",
  };

  const modeTabStyle = (active: boolean): React.CSSProperties => ({
    background: "none",
    border: "none",
    borderBottom: active ? `2px solid ${colors.active}` : "2px solid transparent",
    color: active ? colors.textLight : colors.textDim,
    cursor: "pointer",
    fontSize: 10,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: 1,
    padding: "0 4px 2px",
    lineHeight: "20px",
  });

  const gitToolbar = (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          padding: "3px 8px",
          height: 28,
          boxSizing: "border-box",
        }}
      >
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <button style={modeTabStyle(gitMode === "uncommitted")} onClick={() => setGitMode("uncommitted")}>Uncommitted Diff</button>
          <button style={modeTabStyle(gitMode === "branches")} onClick={() => setGitMode("branches")}>Branches Diff</button>
          <button style={modeTabStyle(gitMode === "commits")} onClick={() => setGitMode("commits")}>Commits</button>
          {hasBranch && <button style={modeTabStyle(gitMode === "branchlist")} onClick={() => setGitMode("branchlist")}>Branches</button>}
          {hasBranch && <button style={modeTabStyle(gitMode === "worktrees")} onClick={() => setGitMode("worktrees")}>Worktrees</button>}
          {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && totalFiles > 0 && <span style={{ fontSize: 10, color: colors.textDim }}>{totalFiles}</span>}
          {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && (totalAdd > 0 || totalDel > 0) && (
            <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
              <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>{" "}
              <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
            </span>
          )}
        </span>
        <div style={{ flex: 1 }} />
        {roots.length > 1 && (gitMode === "uncommitted" || gitMode === "branches" || gitMode === "commits") && (
          <select
            value={rootIndex}
            onChange={(e) => setRootIndex(Number(e.target.value))}
            style={selectStyle}
            title="Workspace root"
            data-testid="git-panel-root-select"
          >
            {roots.map((r) => (
              <option key={r.index} value={r.index} title={r.path}>{r.path}</option>
            ))}
          </select>
        )}
        {pr && <PRChip pr={pr} colors={colors} />}
        {gitMode !== "worktrees" && gitMode !== "branchlist" && (
          <button onClick={load} title="Refresh" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12a9 9 0 1 1-3-6.7" /><polyline points="21,3 21,9 15,9" /></svg>
          </button>
        )}
      </div>
      {gitMode === "branches" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 8px", fontSize: 11, color: colors.textDim }}>
          <select
            value={targetBranch}
            onChange={(e) => setTargetBranch(e.target.value)}
            style={selectStyle}
            title="Changes from"
          >
            {!targetBranch && <option value="">from…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
          <span style={{ color: colors.textDim, fontSize: 11, flexShrink: 0 }}>→</span>
          <select
            value={sourceBranch}
            onChange={(e) => setSourceBranch(e.target.value)}
            style={selectStyle}
            title="Land into"
          >
            {!sourceBranch && <option value="">into…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
      {gitMode === "commits" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 8px", fontSize: 11, color: colors.textDim }}>
          <select
            value={commitBranch}
            onChange={(e) => setCommitBranch(e.target.value)}
            style={selectStyle}
            title="Branch"
          >
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
    </div>
  );

  const diffContent = (
    <DiffViewer
      key={diffVersion}
      channelId={channelId}
      files={data?.files ?? []}
      parsedFiles={parsedFiles}
      expandedFiles={expandedFiles}
      loading={loading}
      hasData={data !== null}
      totalFiles={totalFiles}
      onToggleFile={toggleFile}
      onExpandAll={expandAll}
      onCollapseAll={collapseAll}
      onFileContextMenu={handleFileContextMenu}
    />
  );

  const commitsContent = (
    <CommitHistory
      commits={commits}
      commitsLoading={commitsLoading}
      onLoadMore={loadMoreCommits}
    />
  );

  const worktreesContent = channelId ? (
    <WorktreesPanel
      channelId={channelId}
      isWorktree={isWorktree ?? false}
      hasBranch={hasBranch ?? false}
      onImportWorktree={onImportWorktree}
      onSelectThread={onSelectThread}
    />
  ) : null;

  const branchesContent = channelId ? (
    <BranchesPanel
      channelId={channelId}
      isWorktree={isWorktree ?? false}
      hasBranch={hasBranch ?? false}
      onSelectThread={onSelectThread}
      onBranchChanged={onStatusChange}
    />
  ) : null;

  const contextMenuOverlay = fileContextMenu && (
    <ContextMenu
      x={fileContextMenu.x}
      y={fileContextMenu.y}
      items={[
        ...(channelId
          ? [{
              label: "Open file",
              onClick: () => {
                const detail: FileLinkOpenDetail = {
                  channelId,
                  target: { rootIndex: 0, relPath: fileContextMenu.path },
                  line: null,
                };
                window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
              },
            }]
          : []),
        { label: "Copy relative path", onClick: () => navigator.clipboard.writeText(fileContextMenu.path) },
        { label: "Copy absolute path", onClick: () => navigator.clipboard.writeText((dirPath || "") + "/" + fileContextMenu.path) },
      ]}
      onClose={() => setFileContextMenu(null)}
    />
  );

  if (embedded) {
    return (
      <div data-testid="git-panel" style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar, zoom: fontSizes.panels / 12 }}>
        {gitToolbar}
        {gitMode === "branchlist" ? branchesContent : gitMode === "worktrees" ? worktreesContent : gitMode === "commits" ? commitsContent : diffContent}
        {contextMenuOverlay}
      </div>
    );
  }

  return (
    <div
      data-testid="git-panel"
      ref={panelRef}
      style={{
        width: maximized ? "100%" : width,
        minWidth: maximized ? 0 : MIN_WIDTH,
        maxWidth: maximized ? "none" : `${MAX_WIDTH_PERCENT * 100}vw`,
        flex: maximized ? 1 : undefined,
        flexShrink: maximized ? undefined : 1,
        backgroundColor: colors.sidebar,
        zoom: fontSizes.panels / 12,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        userSelect: resizing ? "none" : undefined,
        borderLeft: maximized ? "none" : `1px solid ${colors.border}`,
      }}
    >
      {/* Resize handle (left edge) — hidden when maximized */}
      {!maximized && (
        <div
          onMouseDown={handleMouseDown}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: 4,
            height: "100%",
            cursor: "col-resize",
            backgroundColor: resizing ? colors.textDim : "transparent",
            zIndex: 1,
          }}
          onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim; }}
          onMouseLeave={(e) => { if (!resizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent"; }}
        />
      )}

      {/* Drag region for macOS title bar alignment */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: maximized && !sidebarOpen ? 76 : maximized ? 4 : 0,
          WebkitAppRegion: "drag",
        }}
      >
        {maximized && onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {maximized && onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
        {maximized && dirPath && (
          <span
            style={{
              fontSize: 12,
              color: colors.textDim,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              minWidth: 0,
              display: "flex",
              alignItems: "center",
              gap: 6,
              marginLeft: 12,
            }}
          >
            {dirPath}
            {branch && (
              <>
                <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                <span style={{ fontSize: 11, color: colors.active, fontFamily: fonts.mono, flexShrink: 0 }}>
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
                    <line x1="6" y1="3" x2="6" y2="15" />
                    <circle cx="18" cy="6" r="3" />
                    <circle cx="6" cy="18" r="3" />
                    <path d="M18 9a9 9 0 0 1-9 9" />
                  </svg>
                  {branch}
                </span>
              </>
            )}
            {pr && <PRChip pr={pr} colors={colors} />}
          </span>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Header — sized to match the main toolbar height so bottom borders align */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
        }}
      >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          flexShrink: 0,
          boxSizing: "border-box",
          height: 35,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          {maximized && tabBar}
          {!maximized && (
            <>
              <button style={modeTabStyle(gitMode === "uncommitted")} onClick={() => setGitMode("uncommitted")}>Uncommitted Diff</button>
              <button style={modeTabStyle(gitMode === "branches")} onClick={() => setGitMode("branches")}>Branches Diff</button>
              <button style={modeTabStyle(gitMode === "commits")} onClick={() => setGitMode("commits")}>Commits</button>
              {hasBranch && <button style={modeTabStyle(gitMode === "branchlist")} onClick={() => setGitMode("branchlist")}>Branches</button>}
          {hasBranch && <button style={modeTabStyle(gitMode === "worktrees")} onClick={() => setGitMode("worktrees")}>Worktrees</button>}
              {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && totalFiles > 0 && (
                <span style={{ fontSize: 10, color: colors.textDim }}>
                  {totalFiles}
                </span>
              )}
              {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && (totalAdd > 0 || totalDel > 0) && (
                <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
                  <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>
                  {" "}
                  <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
                </span>
              )}
            </>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
          {maximized && (
            <span style={{ display: "flex", alignItems: "center", gap: 8, marginRight: 8 }}>
              <button style={modeTabStyle(gitMode === "uncommitted")} onClick={() => setGitMode("uncommitted")}>Uncommitted Diff</button>
              <button style={modeTabStyle(gitMode === "branches")} onClick={() => setGitMode("branches")}>Branches Diff</button>
              <button style={modeTabStyle(gitMode === "commits")} onClick={() => setGitMode("commits")}>Commits</button>
              {hasBranch && <button style={modeTabStyle(gitMode === "branchlist")} onClick={() => setGitMode("branchlist")}>Branches</button>}
          {hasBranch && <button style={modeTabStyle(gitMode === "worktrees")} onClick={() => setGitMode("worktrees")}>Worktrees</button>}
              {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && totalFiles > 0 && (
                <span style={{ fontSize: 10, color: colors.textDim }}>
                  {totalFiles}
                </span>
              )}
              {gitMode !== "commits" && gitMode !== "worktrees" && gitMode !== "branchlist" && (totalAdd > 0 || totalDel > 0) && (
                <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
                  <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>
                  {" "}
                  <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
                </span>
              )}
            </span>
          )}
          {gitMode !== "worktrees" && (
            <button onClick={load} title="Refresh" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21 12a9 9 0 1 1-3-6.7" />
                <polyline points="21,3 21,9 15,9" />
              </svg>
            </button>
          )}
          {onToggleMaximize && (
            <button onClick={onToggleMaximize} title={maximized ? "Restore panel" : "Maximize panel"} style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
              {maximized ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4,14 10,14 10,20" />
                  <polyline points="20,10 14,10 14,4" />
                  <line x1="14" y1="10" x2="21" y2="3" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="15,3 21,3 21,9" />
                  <polyline points="9,21 3,21 3,15" />
                  <line x1="21" y1="3" x2="14" y2="10" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              )}
            </button>
          )}
          <button onClick={onClose} title="Close panel" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
      {gitMode === "branches" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 12px", fontSize: 11, color: colors.textDim }}>
          <select
            value={targetBranch}
            onChange={(e) => setTargetBranch(e.target.value)}
            style={selectStyle}
            title="Changes from"
          >
            {!targetBranch && <option value="">from…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
          <span style={{ color: colors.textDim, fontSize: 11, flexShrink: 0 }}>→</span>
          <select
            value={sourceBranch}
            onChange={(e) => setSourceBranch(e.target.value)}
            style={selectStyle}
            title="Land into"
          >
            {!sourceBranch && <option value="">into…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
      {gitMode === "commits" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 12px", fontSize: 11, color: colors.textDim }}>
          <select
            value={commitBranch}
            onChange={(e) => setCommitBranch(e.target.value)}
            style={selectStyle}
            title="Branch"
          >
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
      </div>

      {/* File list + diffs / Commits / Worktrees */}
      {gitMode === "branchlist" ? branchesContent : gitMode === "worktrees" ? worktreesContent : gitMode === "commits" ? commitsContent : diffContent}
      {contextMenuOverlay}
    </div>
  );
}

// PRChip is a small inline button linking to the open PR. State badge mirrors
// GitHub: green dot for open, grey for draft, purple for merged, red for
// closed. Clicking opens the PR URL in the user's browser.
function PRChip({ pr, colors }: { pr: PRInfo; colors: ColorPalette }) {
  const dot = pr.is_draft
    ? colors.textDim
    : pr.state === "MERGED"
      ? "#a371f7"
      : pr.state === "CLOSED"
        ? "#cf222e"
        : "#3fb950";
  const label = pr.is_draft ? "draft" : pr.state.toLowerCase();
  const title = pr.title ? `#${pr.number} ${pr.title} (${label})` : `#${pr.number} (${label})`;
  return (
    <a
      href={pr.url}
      onClick={(e) => {
        e.preventDefault();
        window.open(pr.url, "_blank", "noopener,noreferrer");
      }}
      title={title}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        padding: "1px 6px",
        borderRadius: 10,
        border: `1px solid ${colors.border}`,
        background: colors.surface,
        color: colors.textLight,
        fontSize: 10,
        fontFamily: fonts.mono,
        textDecoration: "none",
        flexShrink: 0,
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: 3, background: dot, flexShrink: 0 }} />
      PR #{pr.number}
    </a>
  );
}
