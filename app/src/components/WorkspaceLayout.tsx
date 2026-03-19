import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { Channel } from "../types";
import type { SessionStatus } from "../types";
import type { PaneNode, LeafNode, PanelType, SplitDirection, DropPosition } from "../splitPane/types";
import { makeLeaf, findLeafById, splitLeaf, removeLeaf, updateFlex, swapLeavesInTree, moveLeaf, leafCount, collectLeaves, canAddPanel, hasAgentLeaf, collectPanelTypes } from "../splitPane/treeOps";
import { saveLayout, clearLayout, saveActiveLayout, deleteLayout, renameLayout, loadChannelLayouts, ensureDefaultLayouts, createDefaultLayouts, restoreDefaultLayouts, DEFAULT_LAYOUT_NAMES } from "../splitPane/persistence";
import { SplitPaneLayout } from "../splitPane/SplitPaneLayout";
import { PaneLeafHeader } from "../splitPane/PaneLeafHeader";
import { EmptyLayoutPicker } from "../splitPane/AddPanelButton";
import { Terminal, getCloseForInstance } from "./Terminal";
import { ChatView } from "./ChatView";
import { EditorPanel } from "./EditorPanel";
import { MemoryPanel } from "./MemoryPanel";
import { DiffPanel } from "./DiffPanel";
import { BrowserPanel } from "./BrowserPanel";
import { killAgentContainer, fetchBranches, switchBranch, type BranchInfo } from "../api/loopApi";
import { useChatState } from "../hooks/useChatState";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";

type AgentState = "running" | "stopped" | "none";

/** Per-storageKey counters so IDs don't collide across channels. */
const idCounters = new Map<string, number>();

function nextId(channelId: string, panel: PanelType): string {
  const key = `layout-${channelId}`;
  const n = idCounters.get(key) ?? 0;
  idCounters.set(key, n + 1);
  return `${panel}-${n}`;
}

function initIdCounter(channelId: string, tree: PaneNode) {
  const key = `layout-${channelId}`;
  const cur = idCounters.get(key) ?? 0;
  let max = 0;
  for (const leaf of collectLeaves(tree)) {
    const parts = leaf.id.split("-");
    const num = parseInt(parts[parts.length - 1] ?? "0", 10);
    if (!isNaN(num) && num > max) max = num;
  }
  // Only increase, never decrease (other layouts may have higher IDs)
  if (max + 1 > cur) idCounters.set(key, max + 1);
}

function leafIdForPanel(channelId: string, panel: PanelType): string {
  if (panel === "chat" || panel === "editor" || panel === "memory" || panel === "diff") {
    return panel;
  }
  return nextId(channelId, panel);
}

function buildLayoutMenuItemStyle(colors: ColorPalette): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: 6,
    width: "100%",
    padding: "4px 8px",
    border: "none",
    background: "transparent",
    color: colors.textLight,
    fontSize: 11,
    textAlign: "left",
    cursor: "pointer",
    borderRadius: 4,
    fontFamily: fonts.sans,
    whiteSpace: "nowrap",
  };
}

function buildTabButtonStyle(colors: ColorPalette, active: boolean): React.CSSProperties {
  return {
    background: active ? colors.surface : "transparent",
    border: active ? `1px solid ${colors.textLight}` : "1px solid transparent",
    color: active ? colors.textLight : colors.textDim,
    cursor: "pointer",
    padding: "3px 10px",
    minWidth: 70,
    fontSize: 10,
    fontFamily: fonts.mono,
    lineHeight: 1,
    borderRadius: 12,
    display: "flex",
    alignItems: "center",
    gap: 4,
  };
}


// ── Header Branch Picker ──

function HeaderBranchPicker({ channelId, branch, onBranchChanged, onCreateWorktree, onImportWorktree, onSelectThread, onError }: {
  channelId: string;
  branch: string;
  onBranchChanged?: () => void;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
  onError?: (msg: string) => void;
}) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const [branchInfo, setBranchInfo] = useState<BranchInfo | null>(null);
  const [search, setSearch] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const handleOpen = useCallback(() => {
    setOpen(true);
    setSearch("");
    fetchBranches(channelId).then(setBranchInfo).catch(() => {});
    setTimeout(() => searchRef.current?.focus(), 0);
  }, [channelId]);

  const handleSelect = useCallback(async (b: string) => {
    setOpen(false);
    if (b === branch) return;
    try {
      await switchBranch(channelId, b);
      onBranchChanged?.();
    } catch (e) {
      onError?.(e instanceof Error ? e.message : "Failed to switch branch");
    }
  }, [channelId, branch, onBranchChanged, onError]);

  const filtered = branchInfo?.branches.filter((b) =>
    !search || b.toLowerCase().includes(search.toLowerCase()),
  ) ?? [];

  const lowerSearch = search.toLowerCase();
  const filteredWorktrees = branchInfo?.worktrees.filter((wt) =>
    !search || wt.branch.toLowerCase().includes(lowerSearch) || wt.path.split("/").pop()?.toLowerCase().includes(lowerSearch),
  ) ?? [];

  const hasWorktrees = (branchInfo?.worktrees.length ?? 0) > 0 && (!!onImportWorktree || !!onSelectThread);

  return (
    <div ref={ref} style={{ position: "relative", display: "flex", alignItems: "center" }}>
      <button
        onClick={handleOpen}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 2,
          background: "none",
          border: "none",
          cursor: "pointer",
          padding: 0,
          fontSize: 11,
          color: colors.active,
          fontFamily: fonts.mono,
          flexShrink: 0,
          // @ts-expect-error: WebKit-specific CSS property
          WebkitAppRegion: "no-drag",
        }}
        onMouseEnter={(e) => { e.currentTarget.style.opacity = "0.8"; }}
        onMouseLeave={(e) => { e.currentTarget.style.opacity = "1"; }}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2 }}>
          <line x1="6" y1="3" x2="6" y2="15" />
          <circle cx="18" cy="6" r="3" />
          <circle cx="6" cy="18" r="3" />
          <path d="M18 9a9 9 0 0 1-9 9" />
        </svg>
        {branch}
        <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="2" style={{ opacity: 0.5, marginLeft: 1 }}>
          <polyline points="2,3 5,7 8,3" />
        </svg>
      </button>
      {open && (
        <div
          style={{
            position: "absolute",
            top: "100%",
            left: 0,
            marginTop: 4,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            padding: 0,
            zIndex: 1000,
            minWidth: 340,
            maxHeight: hasWorktrees ? undefined : "min(400px, 70vh)",
            height: hasWorktrees ? "min(400px, 70vh)" : undefined,
            display: "flex",
            flexDirection: "column",
            boxShadow: `0 4px 12px ${colors.shadow}`,
          }}
        >
          {/* Search */}
          <div style={{ padding: "8px 8px 4px", flexShrink: 0 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 8px", backgroundColor: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 6 }}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="11" cy="11" r="8" />
                <line x1="21" y1="21" x2="16.65" y2="16.65" />
              </svg>
              <input
                ref={searchRef}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={hasWorktrees ? "Search branches & worktrees" : "Search branches"}
                style={{
                  flex: 1,
                  background: "none",
                  border: "none",
                  outline: "none",
                  color: colors.textLight,
                  fontSize: 12,
                  fontFamily: fonts.sans,
                }}
              />
            </div>
          </div>
          {/* Branches section */}
          <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "4px 0" }}>
            <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
              Branches
            </div>
            {filtered.map((b) => {
              const isCurrent = b === (branchInfo?.current ?? branch);
              return (
                <div
                  key={b}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    borderRadius: 4,
                  }}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
                >
                  <button
                    onClick={() => handleSelect(b)}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      flex: 1,
                      padding: "5px 0 5px 12px",
                      border: "none",
                      background: "transparent",
                      color: isCurrent ? colors.textLight : colors.text,
                      cursor: "pointer",
                      fontSize: 12,
                      fontFamily: fonts.mono,
                      textAlign: "left",
                      whiteSpace: "nowrap",
                    }}
                  >
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                      <line x1="6" y1="3" x2="6" y2="15" />
                      <circle cx="18" cy="6" r="3" />
                      <circle cx="6" cy="18" r="3" />
                      <path d="M18 9a9 9 0 0 1-9 9" />
                    </svg>
                    <span style={{ flex: 1 }}>{b}</span>
                    {isCurrent && <span style={{ color: colors.active, flexShrink: 0 }}>&#10003;</span>}
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(b); }}
                    title="Copy branch name"
                    style={{
                      padding: "2px 6px",
                      border: "none",
                      background: "transparent",
                      color: colors.textDim,
                      cursor: "pointer",
                      borderRadius: 4,
                      flexShrink: 0,
                      fontSize: 10,
                      fontFamily: fonts.mono,
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
                  >
                    cp
                  </button>
                  {onCreateWorktree && (
                    <button
                      onClick={(e) => { e.stopPropagation(); setOpen(false); onCreateWorktree(channelId, b); }}
                      title={`New worktree thread from ${b}`}
                      style={{
                        padding: "2px 6px",
                        border: "none",
                        background: "transparent",
                        color: colors.textDim,
                        cursor: "pointer",
                        borderRadius: 4,
                        flexShrink: 0,
                        fontSize: 10,
                        fontFamily: fonts.mono,
                      }}
                      onMouseEnter={(e) => { e.currentTarget.style.color = colors.active; }}
                      onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
                    >
                      +wt
                    </button>
                  )}
                </div>
              );
            })}
            {filtered.length === 0 && (
              <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No branches found</div>
            )}
          </div>
          {/* Worktrees section */}
          {hasWorktrees && (
            <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "4px 0", borderTop: `1px solid ${colors.border}` }}>
              <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
                Worktrees
              </div>
              {filteredWorktrees.map((wt) => {
                const dirName = wt.path.split("/").pop() || wt.path;
                const hasThread = !!wt.thread_id;
                return (
                  <div
                    key={wt.path}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      borderRadius: 4,
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                    onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
                  >
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 6,
                        flex: 1,
                        padding: "5px 0 5px 12px",
                        fontSize: 12,
                        fontFamily: fonts.mono,
                        whiteSpace: "nowrap",
                        overflow: "hidden",
                        minWidth: 0,
                      }}
                    >
                      {/* Folder icon */}
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                      </svg>
                      <span style={{ color: colors.text, overflow: "hidden", textOverflow: "ellipsis" }}>{wt.branch}</span>
                      <span style={{ color: colors.textDim, fontSize: 10, flexShrink: 0 }}>{dirName}</span>
                    </div>
                    {hasThread && onSelectThread ? (
                      <button
                        onClick={(e) => { e.stopPropagation(); setOpen(false); onSelectThread(wt.thread_id!); }}
                        title="Go to thread"
                        style={{
                          padding: "2px 6px",
                          border: "none",
                          background: "transparent",
                          color: colors.active,
                          cursor: "pointer",
                          borderRadius: 4,
                          flexShrink: 0,
                          fontSize: 10,
                          fontFamily: fonts.mono,
                        }}
                        onMouseEnter={(e) => { e.currentTarget.style.opacity = "0.7"; }}
                        onMouseLeave={(e) => { e.currentTarget.style.opacity = "1"; }}
                      >
                        go
                      </button>
                    ) : onImportWorktree ? (
                      <button
                        onClick={(e) => { e.stopPropagation(); setOpen(false); onImportWorktree(channelId, wt.path); }}
                        title="Import worktree as thread"
                        style={{
                          padding: "2px 6px",
                          border: "none",
                          background: "transparent",
                          color: colors.textDim,
                          cursor: "pointer",
                          borderRadius: 4,
                          flexShrink: 0,
                          fontSize: 10,
                          fontFamily: fonts.mono,
                        }}
                        onMouseEnter={(e) => { e.currentTarget.style.color = colors.active; }}
                        onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
                      >
                        imp
                      </button>
                    ) : null}
                  </div>
                );
              })}
              {filteredWorktrees.length === 0 && (
                <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No worktrees found</div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export interface WorkspaceLayoutRef {
  switchToLayout: (name: string) => void;
}

interface WorkspaceLayoutProps {
  channelId: string;
  channel: Channel;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  onOpenPalette: () => void;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
  openMemoryFile?: string | null;
  onOpenMemoryFileComplete?: () => void;
  onStatusChange?: () => void;
  error?: string | null;
  onDismissError?: () => void;
  diffStats?: { add: number; del: number };
  style?: React.CSSProperties;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
}

export const WorkspaceLayout = forwardRef<WorkspaceLayoutRef, WorkspaceLayoutProps>(function WorkspaceLayout({
  channelId,
  channel,
  sidebarOpen,
  onToggleSidebar,
  onOpenPalette,
  scrollToMessageId,
  onScrollComplete,
  openMemoryFile,
  onOpenMemoryFileComplete,
  onStatusChange,
  error,
  onDismissError,
  diffStats: _diffStats,
  style,
  onCreateWorktree,
  onImportWorktree,
  onSelectThread,
}, ref) {
  const { colors } = useTheme();
  const [branchError, setBranchError] = useState<string | null>(null);

  // --- Named layouts state ---
  const [layoutNames, setLayoutNames] = useState<string[]>(() => {
    return ensureDefaultLayouts(channelId).order;
  });
  const [activeName, setActiveName] = useState<string>(() => {
    return ensureDefaultLayouts(channelId).active;
  });
  const [tree, setTree] = useState<PaneNode | null>(() => {
    const ch = ensureDefaultLayouts(channelId);
    const t = ch.layouts[ch.active] ?? null;
    if (t) initIdCounter(channelId, t);
    return t;
  });
  const treeRef = useRef(tree);
  treeRef.current = tree;

  // Chat state — hoisted here so the WebSocket + messages survive layout switches.
  const chatState = useChatState(channelId, channel.agent_running);

  // Track per-pane session status for aggregate agent state.
  const statusMapRef = useRef(new Map<string, SessionStatus>());
  const [agentState, setAgentState] = useState<AgentState>("none");
  const [showLayoutMenu, setShowLayoutMenu] = useState(false);
  const [maximizedLeafId, setMaximizedLeafId] = useState<string | null>(null);

  // Close layout menu on outside click.
  useEffect(() => {
    if (!showLayoutMenu) return;
    const handler = () => setShowLayoutMenu(false);
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showLayoutMenu]);

  const computeAgentState = useCallback((): AgentState => {
    const current = treeRef.current;
    if (!current) return "none";
    const terminalLeaves = collectLeaves(current).filter((l) => l.panel === "agent" || l.panel === "shell");
    if (terminalLeaves.length === 0) return "none";
    const terminalStatuses = [...statusMapRef.current.entries()]
      .filter(([leafId]) => {
        const p = findLeafById(current, leafId)?.panel;
        return p === "agent" || p === "shell";
      });
    if (terminalStatuses.length === 0) return "running"; // terminal leaves exist but no status yet
    const allDead = terminalStatuses.every(([, s]) => s === "completed" || s === "failed");
    return allDead ? "stopped" : "running";
  }, []);

  const handlePaneStatus = useCallback((id: string, status: SessionStatus) => {
    statusMapRef.current.set(id, status);
    setAgentState(computeAgentState());
  }, [computeAgentState]);

  // Reload when channelId changes.
  const prevChannelRef = useRef(channelId);
  useEffect(() => {
    if (channelId !== prevChannelRef.current) {
      prevChannelRef.current = channelId;
      statusMapRef.current.clear();
      const ch = ensureDefaultLayouts(channelId);
      setLayoutNames(ch.order);
      const name = ch.active;
      setActiveName(name);
      const t = ch.layouts[name] ?? null;
      if (t) initIdCounter(channelId, t);
      setTree(t);
      setAgentState("none");
      setMaximizedLeafId(null);
    }
  }, [channelId]);

  // Save tree whenever it changes.
  useEffect(() => {
    if (tree) {
      saveLayout(channelId, activeName, tree);
    }
  }, [channelId, activeName, tree]);

  // --- Layout tab operations ---
  const switchLayout = useCallback((name: string) => {
    const ch = loadChannelLayouts(channelId);
    const t = ch?.layouts[name] ?? null;
    if (t) initIdCounter(channelId, t);
    statusMapRef.current.clear();
    setActiveName(name);
    setTree(t);
    setAgentState("none");
    setMaximizedLeafId(null);
    saveActiveLayout(channelId, name);
  }, [channelId]);

  useImperativeHandle(ref, () => ({
    switchToLayout: switchLayout,
  }), [switchLayout]);

  // Auto-switch to a layout with a chat pane when scrollToMessageId is set.
  useEffect(() => {
    if (!scrollToMessageId || !tree) return;
    if (collectLeaves(tree).some((l) => l.panel === "chat")) return;
    const saved = loadChannelLayouts(channelId);
    if (!saved) return;
    for (const name of saved.order) {
      const savedTree = saved.layouts[name];
      if (savedTree && collectLeaves(savedTree).some((l) => l.panel === "chat")) {
        switchLayout(name);
        return;
      }
    }
  }, [scrollToMessageId, tree, channelId, switchLayout]);

  // Auto-switch to a layout with a memory pane when openMemoryFile is set.
  useEffect(() => {
    if (!openMemoryFile || !tree) return;
    if (collectLeaves(tree).some((l) => l.panel === "memory")) {
      // Memory pane exists, clear the prop after MemoryPanel consumes it.
      setTimeout(() => onOpenMemoryFileComplete?.(), 0);
      return;
    }
    const saved = loadChannelLayouts(channelId);
    if (!saved) return;
    for (const name of saved.order) {
      const savedTree = saved.layouts[name];
      if (savedTree && collectLeaves(savedTree).some((l) => l.panel === "memory")) {
        switchLayout(name);
        setTimeout(() => onOpenMemoryFileComplete?.(), 0);
        return;
      }
    }
  }, [openMemoryFile, tree, channelId, switchLayout, onOpenMemoryFileComplete]);

  const addLayout = useCallback(() => {
    let n = 1;
    let name = `Layout ${n}`;
    while (layoutNames.includes(name)) { n++; name = `Layout ${n}`; }
    setLayoutNames((prev) => [...prev, name]);
    setActiveName(name);
    setTree(null);
    statusMapRef.current.clear();
    setAgentState("none");
    // Persist the empty slot so the name is saved
    saveActiveLayout(channelId, name);
  }, [channelId, layoutNames]);

  const handleRenameLayout = useCallback((oldName: string, newName: string) => {
    const trimmed = newName.trim();
    if (!trimmed || trimmed === oldName || layoutNames.includes(trimmed)) return;
    renameLayout(channelId, oldName, trimmed);
    setLayoutNames((prev) => prev.map((n) => (n === oldName ? trimmed : n)));
    if (activeName === oldName) setActiveName(trimmed);
  }, [channelId, layoutNames, activeName]);

  const handleDeleteLayout = useCallback((name: string) => {
    if (layoutNames.length <= 1) return;
    // Kill terminal sessions if deleting the active layout
    if (activeName === name) {
      const current = treeRef.current;
      if (current) {
        for (const leaf of collectLeaves(current)) {
          if (leaf.panel === "agent" || leaf.panel === "shell") {
            const target = leaf.panel === "agent" ? "agent" : "host";
            const closeKey = `${target}:${channelId}:${leaf.id}`;
            getCloseForInstance(closeKey)?.();
          }
        }
        if (hasAgentLeaf(current)) killAgentContainer(channelId);
      }
    }
    deleteLayout(channelId, name);
    const remaining = layoutNames.filter((n) => n !== name);
    setLayoutNames(remaining);
    if (activeName === name && remaining.length > 0) {
      const next = remaining[0]!;
      const ch = loadChannelLayouts(channelId);
      const t = ch?.layouts[next] ?? null;
      if (t) initIdCounter(channelId, t);
      setActiveName(next);
      setTree(t);
      statusMapRef.current.clear();
      setAgentState("none");
      saveActiveLayout(channelId, next);
    }
  }, [channelId, layoutNames, activeName]);

  // --- Tree operations ---
  const handleDrop = useCallback(
    (dragId: string, dropId: string, position: DropPosition) => {
      if (dragId === dropId) return;
      setTree((prev) => {
        if (!prev) return prev;
        if (position === "center") {
          return swapLeavesInTree(prev, dragId, dropId);
        }
        return moveLeaf(prev, dragId, dropId, position);
      });
    },
    [],
  );

  const handleUpdateFlex = useCallback((parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => {
    setTree((prev) => prev ? updateFlex(prev, parentPath, dividerIndex, flexA, flexB) : prev);
  }, []);

  const handleRemoveLeaf = useCallback(
    (id: string) => {
      const current = treeRef.current;
      if (!current) return;
      const leaf = findLeafById(current, id);
      const wasAgent = leaf?.panel === "agent";
      if (leaf && (leaf.panel === "agent" || leaf.panel === "shell")) {
        const target = leaf.panel === "agent" ? "agent" : "host";
        const closeKey = `${target}:${channelId}:${id}`;
        getCloseForInstance(closeKey)?.();
      }
      statusMapRef.current.delete(id);
      setMaximizedLeafId((prev) => prev === id ? null : prev);
      setTree((prev) => {
        if (!prev) return prev;
        if (leafCount(prev) <= 1) {
          clearLayout(channelId, activeName);
          if (wasAgent) killAgentContainer(channelId);
          setAgentState("none");
          return null;
        }
        const newTree = removeLeaf(prev, id) ?? null;
        if (wasAgent && newTree && !hasAgentLeaf(newTree)) {
          killAgentContainer(channelId);
          setAgentState("none");
        } else {
          setAgentState(computeAgentState());
        }
        return newTree;
      });
    },
    [channelId, activeName, computeAgentState],
  );

  const handleSplitLeaf = useCallback(
    (leafId: string, panel: PanelType, direction: SplitDirection) => {
      setTree((prev) => {
        if (!prev) return prev;
        if (!canAddPanel(prev, panel)) return prev;
        return splitLeaf(prev, leafId, direction, makeLeaf(leafIdForPanel(channelId, panel), panel));
      });
    },
    [channelId],
  );

  const handleEmptyAdd = useCallback(
    (panel: PanelType) => {
      const newTree = makeLeaf(leafIdForPanel(channelId, panel), panel);
      setTree(newTree);
      // If this is the first panel in a new layout, ensure it's persisted with a name
      if (!layoutNames.includes(activeName)) {
        setLayoutNames((prev) => [...prev, activeName]);
      }
      saveLayout(channelId, activeName, newTree);
    },
    [channelId, activeName, layoutNames],
  );

  const handleKillAgents = useCallback(() => {
    // Close all agent and shell terminal sessions in the current tree.
    const current = treeRef.current;
    if (current) {
      for (const leaf of collectLeaves(current)) {
        if (leaf.panel === "agent" || leaf.panel === "shell") {
          const target = leaf.panel === "agent" ? "agent" : "host";
          const closeKey = `${target}:${channelId}:${leaf.id}`;
          getCloseForInstance(closeKey)?.();
        }
      }
    }
    killAgentContainer(channelId);
  }, [channelId]);

  const hasMissingDefaults = (DEFAULT_LAYOUT_NAMES as readonly string[]).some((n) => !layoutNames.includes(n));
  const isDefaultLayout = (DEFAULT_LAYOUT_NAMES as readonly string[]).includes(activeName);

  const restoreDefaults = useCallback(() => {
    const ch = restoreDefaultLayouts(channelId);
    setLayoutNames(ch.order);
  }, [channelId]);

  const handleResetLayout = useCallback(() => {
    // Close any running terminal sessions in the current tree
    const current = treeRef.current;
    if (current) {
      for (const leaf of collectLeaves(current)) {
        if (leaf.panel === "agent" || leaf.panel === "shell") {
          const target = leaf.panel === "agent" ? "agent" : "host";
          const closeKey = `${target}:${channelId}:${leaf.id}`;
          getCloseForInstance(closeKey)?.();
        }
      }
      if (hasAgentLeaf(current)) {
        killAgentContainer(channelId);
      }
    }
    const defaults = createDefaultLayouts();
    const defaultTree = defaults.layouts[activeName] ?? null;
    if (defaultTree) {
      saveLayout(channelId, activeName, defaultTree);
      initIdCounter(channelId, defaultTree);
    } else {
      clearLayout(channelId, activeName);
    }
    statusMapRef.current.clear();
    setTree(defaultTree);
    setAgentState("none");
  }, [channelId, activeName]);

  const dirPath = channel.dir_path || "";
  const branch = channel.branch || "";
  const commit = channel.commit || "";

  const renderLeaf = useCallback(
    (leaf: LeafNode): React.ReactNode => {
      switch (leaf.panel) {
        case "chat":
          return (
            <ChatView
              key={`layout-chat-${channelId}`}
              channelId={channelId}
              chatState={chatState}
              scrollToMessageId={scrollToMessageId}
              onScrollComplete={onScrollComplete}
            />
          );
        case "editor":
          return (
            <EditorPanel
              key={`layout-editor-${channelId}`}
              channelId={channelId}
              dirPath={dirPath}
              branch={branch}
              embedded
              tabsStorageKey="loop-layout-editor-tabs"
              onClose={() => handleRemoveLeaf(leaf.id)}
            />
          );
        case "memory":
          return (
            <MemoryPanel
              key={`layout-memory-${channelId}`}
              channelId={channelId}
              dirPath={dirPath}
              branch={branch}
              embedded
              openMemoryFile={openMemoryFile}
              onClose={() => handleRemoveLeaf(leaf.id)}
            />
          );
        case "diff":
          return (
            <DiffPanel
              key={`layout-diff-${channelId}`}
              channelId={channelId}
              dirPath={dirPath}
              branch={branch}
              embedded
              onClose={() => handleRemoveLeaf(leaf.id)}
            />
          );
        case "agent":
          return (
            <div key={`layout-agent-${channelId}-${leaf.id}`} style={{ flex: 1, display: "flex", flexDirection: "column", backgroundColor: colors.sidebar }}>
              <Terminal
                channelId={channelId}
                target="agent"
                instanceId={leaf.id}
                hideActions
                onStatusChange={onStatusChange}
                onPaneStatus={(status) => handlePaneStatus(leaf.id, status)}
              />
            </div>
          );
        case "shell":
          return (
            <div key={`layout-shell-${channelId}-${leaf.id}`} style={{ flex: 1, display: "flex", flexDirection: "column", backgroundColor: colors.sidebar }}>
              <Terminal
                channelId={channelId}
                target="host"
                instanceId={leaf.id}
                hideActions
                onStatusChange={onStatusChange}
                onPaneStatus={(status) => handlePaneStatus(leaf.id, status)}
              />
            </div>
          );
        case "browser":
          return (
            <BrowserPanel
              key={`layout-browser-${channelId}`}
              channelId={channelId}
            />
          );
        default:
          return null;
      }
    },
    [channelId, chatState, dirPath, branch, scrollToMessageId, onScrollComplete, openMemoryFile, onStatusChange, handlePaneStatus, handleRemoveLeaf],
  );

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.sidebar,
        overflow: "hidden",
        ...style,
      }}
    >
      {/* Drag region */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: !sidebarOpen ? 76 : 4,
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      >
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
            // @ts-expect-error: WebKit-specific CSS property
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
            // @ts-expect-error: WebKit-specific CSS property
            WebkitAppRegion: "no-drag",
          }}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
        </button>
        {dirPath && (
          <>
            <span
              onDoubleClick={(e) => { navigator.clipboard.writeText(dirPath); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
              title="Double-click to copy path"
              style={{
                fontSize: 12,
                color: colors.textDim,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                minWidth: 0,
                marginLeft: 12,
                cursor: "default",
                // @ts-expect-error: WebKit-specific CSS property
                WebkitAppRegion: "no-drag",
              }}
            >
              {dirPath}
            </span>
            {commit && (
              <>
                <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px 0 12px" }}>|</span>
                <span
                  onDoubleClick={(e) => { navigator.clipboard.writeText(commit); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
                  title="Double-click to copy commit hash"
                  style={{
                    fontSize: 11,
                    color: colors.textDim,
                    fontFamily: fonts.mono,
                    flexShrink: 0,
                    cursor: "default",
                    // @ts-expect-error: WebKit-specific CSS property
                    WebkitAppRegion: "no-drag",
                  }}
                >
                  {commit}
                </span>
              </>
            )}
            {branch && (
              <>
                <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px" }}>|</span>
                {channel.worktree ? (
                  <span
                    style={{ fontSize: 11, color: colors.active, fontFamily: fonts.mono, flexShrink: 0, cursor: "default" }}
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
                      <line x1="6" y1="3" x2="6" y2="15" />
                      <circle cx="18" cy="6" r="3" />
                      <circle cx="6" cy="18" r="3" />
                      <path d="M18 9a9 9 0 0 1-9 9" />
                    </svg>
                    {branch}
                  </span>
                ) : (
                  <HeaderBranchPicker channelId={channelId} branch={branch} onBranchChanged={onStatusChange} onCreateWorktree={channel.parent_id ? undefined : onCreateWorktree} onImportWorktree={channel.parent_id ? undefined : onImportWorktree} onSelectThread={channel.parent_id ? undefined : onSelectThread} onError={setBranchError} />
                )}
              </>
            )}
          </>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Error bar */}
      {(error || branchError) && (
        <div
          role="alert"
          style={{
            padding: "8px 12px",
            backgroundColor: colors.errorBannerBg,
            color: colors.errorBannerText,
            fontSize: "13px",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span>{branchError || error}</span>
          <button
            onClick={() => { setBranchError(null); onDismissError?.(); }}
            style={{
              background: "none",
              border: "none",
              color: colors.errorBannerText,
              cursor: "pointer",
              fontSize: "16px",
            }}
          >
            &times;
          </button>
        </div>
      )}

      {/* Layout buttons & tabs */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "0 8px",
          borderBottom: `1px solid ${colors.border}`,
          height: 35,
          boxSizing: "border-box",
          gap: 4,
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
            flexShrink: 0,
          }}
        >
          Layouts
        </span>
        {layoutNames.map((name) => (
          <LayoutTab
            key={name}
            name={name}
            active={name === activeName}
            canDelete={layoutNames.length > 1}
            onSelect={() => { if (name !== activeName) switchLayout(name); }}
            onRename={(newName) => handleRenameLayout(name, newName)}
            onDelete={() => handleDeleteLayout(name)}
          />
        ))}
        <button
          onClick={addLayout}
          title="New layout"
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
            cursor: "pointer",
            padding: "4px",
            lineHeight: 1,
            borderRadius: 12,
            display: "flex",
            alignItems: "center",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; e.currentTarget.style.background = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; e.currentTarget.style.background = "none"; }}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
        </button>
        <div style={{ flex: 1 }} />
        {(agentState === "running" || channel.container_running) && (
          <button
            onClick={handleKillAgents}
            title="Kill agent containers"
            style={{
              background: "none",
              border: `1px solid ${colors.error}`,
              color: colors.error,
              cursor: "pointer",
              padding: "2px 8px",
              fontSize: 10,
              fontFamily: fonts.mono,
              lineHeight: 1,
              borderRadius: 10,
              display: "flex",
              alignItems: "center",
              gap: 4,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.error; e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.error; }}
          >
            Kill
          </button>
        )}
        {(hasMissingDefaults || isDefaultLayout) && <div style={{ position: "relative" }}>
          <button
            onClick={() => setShowLayoutMenu((v) => !v)}
            title="Layout options"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              fontSize: 10,
              fontFamily: fonts.mono,
              lineHeight: 1,
              borderRadius: 10,
              display: "flex",
              alignItems: "center",
              gap: 4,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; e.currentTarget.style.borderColor = colors.textDim; }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; e.currentTarget.style.borderColor = colors.border; }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M1 4v6h6" />
              <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" />
            </svg>
            Reset
          </button>
          {showLayoutMenu && (
            <div
              style={{
                position: "absolute",
                top: "100%",
                right: 0,
                marginTop: 4,
                backgroundColor: colors.surface,
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                padding: 4,
                zIndex: 1000,
                minWidth: 150,
                boxShadow: `0 4px 12px ${colors.shadow}`,
                fontFamily: fonts.sans,
              }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              {hasMissingDefaults && (
                <button
                  onClick={() => { restoreDefaults(); setShowLayoutMenu(false); }}
                  style={buildLayoutMenuItemStyle(colors)}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
                >
                  Restore defaults
                </button>
              )}
              {isDefaultLayout && (
                <button
                  onClick={() => { handleResetLayout(); setShowLayoutMenu(false); }}
                  style={buildLayoutMenuItemStyle(colors)}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
                >
                  Reset current
                </button>
              )}
            </div>
          )}
        </div>}
      </div>

      {/* Layout content */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
        {!tree ? (
          <EmptyLayoutPicker onAdd={handleEmptyAdd} />
        ) : maximizedLeafId && findLeafById(tree, maximizedLeafId) ? (
          (() => {
            const leaf = findLeafById(tree, maximizedLeafId)!;
            const usedSingletons = collectPanelTypes(tree);
            return (
              <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, minWidth: 0 }}>
                <PaneLeafHeader
                  leafId={leaf.id}
                  panel={leaf.panel}
                  usedSingletons={usedSingletons}
                  isMaximized
                  onRemove={() => { setMaximizedLeafId(null); handleRemoveLeaf(leaf.id); }}
                  onDrop={handleDrop}
                  onSplitLeaf={handleSplitLeaf}
                  onToggleMaximize={() => setMaximizedLeafId(null)}
                />
                <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
                  {renderLeaf(leaf)}
                </div>
              </div>
            );
          })()
        ) : (
          <SplitPaneLayout
            tree={tree}
            renderLeaf={renderLeaf}
            onUpdateFlex={handleUpdateFlex}
            onDrop={handleDrop}
            onRemoveLeaf={handleRemoveLeaf}
            onSplitLeaf={handleSplitLeaf}
            onMaximize={(leafId) => setMaximizedLeafId(leafId)}
          />
        )}
      </div>
    </div>
  );
});

// ── Layout Tab ──

function LayoutTab({ name, active, canDelete, onSelect, onRename, onDelete }: {
  name: string;
  active: boolean;
  canDelete: boolean;
  onSelect: () => void;
  onRename: (newName: string) => void;
  onDelete: () => void;
}) {
  const { colors } = useTheme();
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState(name);
  const [confirming, setConfirming] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

  // Close confirm popover on outside click.
  useEffect(() => {
    if (!confirming) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) setConfirming(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirming]);

  const commitRename = () => {
    setEditing(false);
    if (editValue.trim() && editValue.trim() !== name) {
      onRename(editValue.trim());
    } else {
      setEditValue(name);
    }
  };

  return (
    <div
      onClick={onSelect}
      style={{
        ...buildTabButtonStyle(colors, active),
        flexShrink: 0,
        position: "relative",
        paddingLeft: canDelete && !editing ? 4 : 10,
        paddingRight: canDelete && !editing ? 10 : 10,
      }}
      onMouseEnter={(e) => { if (!active) { e.currentTarget.style.background = colors.hoverBg; e.currentTarget.style.color = colors.textLight; } }}
      onMouseLeave={(e) => { if (!active) { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = colors.textDim; } }}
    >
      {canDelete && !editing && (
        <button
          onClick={(e) => { e.stopPropagation(); setConfirming(true); }}
          title="Delete layout"
          style={{
            background: "none",
            border: "none",
            color: active ? colors.textLight : colors.textDim,
            cursor: "pointer",
            padding: 0,
            lineHeight: 1,
            fontSize: 10,
            display: "flex",
            alignItems: "center",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      )}
      {editing ? (
        <input
          ref={inputRef}
          value={editValue}
          onChange={(e) => setEditValue(e.target.value)}
          onBlur={commitRename}
          onKeyDown={(e) => {
            if (e.key === "Enter") commitRename();
            if (e.key === "Escape") { setEditValue(name); setEditing(false); }
          }}
          onClick={(e) => e.stopPropagation()}
          style={{
            background: "none",
            border: `1px solid ${colors.border}`,
            color: colors.textLight,
            fontSize: 10,
            fontFamily: fonts.mono,
            padding: "0 4px",
            borderRadius: 3,
            outline: "none",
            width: Math.max(40, editValue.length * 7),
          }}
        />
      ) : (
        <span
          onDoubleClick={(e) => { e.stopPropagation(); setEditValue(name); setEditing(true); }}
          title="Double-click to rename"
          style={{ flex: 1, textAlign: "center" }}
        >
          {name}
        </span>
      )}
      {confirming && (
        <div
          data-confirm-popover
          onMouseDown={(e) => e.stopPropagation()}
          style={{
            position: "absolute",
            top: "100%",
            left: "50%",
            transform: "translateX(-50%)",
            marginTop: 10,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.textLight}`,
            borderRadius: 6,
            padding: "0 8px",
            height: 22,
            boxSizing: "border-box",
            zIndex: 1000,
            boxShadow: `0 4px 12px ${colors.shadow}`,
            display: "flex",
            alignItems: "center",
            gap: 6,
            whiteSpace: "nowrap",
            fontFamily: fonts.sans,
            fontSize: 9,
          }}
        >
          {/* Arrow pointing up */}
          <svg width="16" height="9" viewBox="0 0 16 9" style={{ position: "absolute", top: -8, left: "50%", transform: "translateX(-50%)", filter: "drop-shadow(0 -2px 4px rgba(0,0,0,0.3))" }}>
            <path d="M1 9 L7 2.5 Q8 1.5 9 2.5 L15 9 Z" fill={colors.surface} stroke={colors.textLight} strokeWidth="0.75" />
            <rect x="0" y="8" width="16" height="2" fill={colors.surface} />
          </svg>
          <span style={{ color: colors.textLight }}>Delete?</span>
          <button
            onClick={(e) => { e.stopPropagation(); setConfirming(false); onDelete(); }}
            style={{
              background: colors.dangerBg,
              border: `1px solid ${colors.dangerText}`,
              color: colors.dangerText,
              cursor: "pointer",
              padding: "1px 6px",
              fontSize: 9,
              fontFamily: fonts.sans,
              borderRadius: 4,
              lineHeight: 1.4,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.background = colors.dangerHoverBg; e.currentTarget.style.color = colors.white; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = colors.dangerBg; e.currentTarget.style.color = colors.dangerText; }}
          >
            Yes
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); setConfirming(false); }}
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "1px 6px",
              fontSize: 9,
              fontFamily: fonts.sans,
              borderRadius: 4,
              lineHeight: 1.4,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; e.currentTarget.style.borderColor = colors.textDim; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; e.currentTarget.style.borderColor = colors.border; }}
          >
            No
          </button>
        </div>
      )}
    </div>
  );
}
