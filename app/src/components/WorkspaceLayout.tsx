import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel } from "../types";
import type { SessionStatus } from "../types";
import type { PaneNode, LeafNode, PanelType, SplitDirection, DropPosition } from "../splitPane/types";
import { makeLeaf, findLeafById, splitLeaf, removeLeaf, updateFlex, swapLeavesInTree, moveLeaf, leafCount, collectLeaves, canAddPanel, hasAgentLeaf } from "../splitPane/treeOps";
import { loadChannelLayouts, saveLayout, clearLayout, saveActiveLayout, deleteLayout, renameLayout } from "../splitPane/persistence";
import { SplitPaneLayout } from "../splitPane/SplitPaneLayout";
import { EmptyLayoutPicker } from "../splitPane/AddPanelButton";
import { Terminal, getCloseForInstance } from "./Terminal";
import { ChatView } from "./ChatView";
import { EditorPanel } from "./EditorPanel";
import { MemoryPanel } from "./MemoryPanel";
import { DiffPanel } from "./DiffPanel";
import { killAgentContainer } from "../api/loopApi";
import { colors, fonts } from "../theme";

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

interface WorkspaceLayoutProps {
  channelId: string;
  channel: Channel;
  sidebarOpen: boolean;
  tabBar: React.ReactNode;
  onToggleSidebar: () => void;
  onOpenPalette: () => void;
  onClose: () => void;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
  onStatusChange?: () => void;
}

export function WorkspaceLayout({
  channelId,
  channel,
  sidebarOpen,
  tabBar,
  onToggleSidebar,
  onOpenPalette,
  onClose,
  scrollToMessageId,
  onScrollComplete,
  onStatusChange,
}: WorkspaceLayoutProps) {
  // --- Named layouts state ---
  const [layoutNames, setLayoutNames] = useState<string[]>(() => {
    const ch = loadChannelLayouts(channelId);
    return ch?.order ?? [];
  });
  const [activeName, setActiveName] = useState<string>(() => {
    const ch = loadChannelLayouts(channelId);
    return ch?.active ?? "Default";
  });
  const [tree, setTree] = useState<PaneNode | null>(() => {
    const ch = loadChannelLayouts(channelId);
    if (!ch) return null;
    const t = ch.layouts[ch.active] ?? null;
    if (t) initIdCounter(channelId, t);
    return t;
  });
  const treeRef = useRef(tree);
  treeRef.current = tree;

  // Track per-pane session status for aggregate agent state.
  const statusMapRef = useRef(new Map<string, SessionStatus>());
  const [agentState, setAgentState] = useState<AgentState>("none");

  const computeAgentState = useCallback((): AgentState => {
    const current = treeRef.current;
    if (!current || !hasAgentLeaf(current)) return "none";
    const agentStatuses = [...statusMapRef.current.entries()]
      .filter(([leafId]) => findLeafById(current, leafId)?.panel === "agent");
    if (agentStatuses.length === 0) return "running"; // agent leaves exist but no status yet
    const allDead = agentStatuses.every(([, s]) => s === "completed" || s === "failed");
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
      const ch = loadChannelLayouts(channelId);
      setLayoutNames(ch?.order ?? []);
      const name = ch?.active ?? "Default";
      setActiveName(name);
      const t = ch?.layouts[name] ?? null;
      if (t) initIdCounter(channelId, t);
      setTree(t);
      setAgentState("none");
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
    saveActiveLayout(channelId, name);
  }, [channelId]);

  const addLayout = useCallback(() => {
    let n = layoutNames.length + 1;
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
    if (layoutNames.length <= 1) return; // don't delete last layout
    deleteLayout(channelId, name);
    const remaining = layoutNames.filter((n) => n !== name);
    setLayoutNames(remaining);
    if (activeName === name) {
      const next = remaining[0]!;
      const ch = loadChannelLayouts(channelId);
      const t = ch?.layouts[next] ?? null;
      if (t) initIdCounter(channelId, t);
      setActiveName(next);
      setTree(t);
      statusMapRef.current.clear();
      setAgentState("none");
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
    killAgentContainer(channelId);
  }, [channelId]);

  const dirPath = channel.dir_path || "";
  const branch = channel.branch || "";

  const renderLeaf = useCallback(
    (leaf: LeafNode): React.ReactNode => {
      switch (leaf.panel) {
        case "chat":
          return (
            <ChatView
              key={`layout-chat-${channelId}`}
              channelId={channelId}
              initialRunningBot={channel.agent_running}
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
                onStatusChange={onStatusChange}
              />
            </div>
          );
        default:
          return null;
      }
    },
    [channelId, channel.agent_running, dirPath, branch, scrollToMessageId, onScrollComplete, onStatusChange, handlePaneStatus, handleRemoveLeaf],
  );

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.sidebar,
        overflow: "hidden",
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
          </span>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Tab bar + controls */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "3px 8px",
          borderBottom: `1px solid ${colors.border}`,
          height: 39,
          boxSizing: "border-box",
          gap: 8,
        }}
      >
        {tabBar}
        <div style={{ flex: 1 }} />
        {agentState === "running" && (
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
              borderRadius: 6,
              display: "flex",
              alignItems: "center",
              gap: 4,
            }}
          >
            Kill
          </button>
        )}
        <button
          onClick={onClose}
          title="Close layout"
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
            cursor: "pointer",
            padding: 4,
            lineHeight: 1,
            borderRadius: 4,
            display: "flex",
            alignItems: "center",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* Layout tabs */}
      {layoutNames.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            padding: "0 8px",
            borderBottom: `1px solid ${colors.border}`,
            height: 30,
            flexShrink: 0,
            gap: 0,
            overflow: "hidden",
          }}
        >
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
              padding: "2px 6px",
              lineHeight: 1,
              fontSize: 12,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              marginLeft: 2,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
          </button>
        </div>
      )}

      {/* Layout content */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
        {!tree ? (
          <EmptyLayoutPicker onAdd={handleEmptyAdd} />
        ) : (
          <SplitPaneLayout
            tree={tree}
            renderLeaf={renderLeaf}
            onUpdateFlex={handleUpdateFlex}
            onDrop={handleDrop}
            onRemoveLeaf={handleRemoveLeaf}
            onSplitLeaf={handleSplitLeaf}
          />
        )}
      </div>
    </div>
  );
}

// ── Layout Tab ──

function LayoutTab({ name, active, canDelete, onSelect, onRename, onDelete }: {
  name: string;
  active: boolean;
  canDelete: boolean;
  onSelect: () => void;
  onRename: (newName: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState(name);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

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
        display: "flex",
        alignItems: "center",
        gap: 4,
        padding: "2px 8px",
        fontSize: 11,
        cursor: "pointer",
        color: active ? colors.textLight : colors.textDim,
        borderBottom: active ? `2px solid ${colors.active}` : "2px solid transparent",
        height: "100%",
        boxSizing: "border-box",
        flexShrink: 0,
      }}
      onMouseEnter={(e) => { if (!active) (e.currentTarget as HTMLDivElement).style.color = colors.textLight; }}
      onMouseLeave={(e) => { if (!active) (e.currentTarget as HTMLDivElement).style.color = colors.textDim; }}
    >
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
            fontSize: 11,
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
        >
          {name}
        </span>
      )}
      {canDelete && !editing && (
        <button
          onClick={(e) => { e.stopPropagation(); onDelete(); }}
          title="Delete layout"
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
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
    </div>
  );
}
