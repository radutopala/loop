import { useCallback, useRef, useState } from "react";
import type { Channel, ImageBuildStatusData, ImageUpdateAvailableData, UpdateStatus } from "../../types";
import { useTheme } from "../../ThemeContext";
import { ContextMenu } from "../shared/ContextMenu";
import type { MenuItem } from "../shared/ContextMenu";
import { SidebarHeader } from "./SidebarHeader";
import { ChannelList } from "./ChannelList";
import { SidebarFooter } from "./SidebarFooter";
import { RenameThreadDialog } from "./RenameThreadDialog";
import { storageGetJSON, storageSetJSON } from "../../utils/storage";

const MIN_WIDTH = 180;
const MAX_WIDTH_PERCENT = 0.25;
const DEFAULT_WIDTH = 280;
const ORDER_STORAGE_KEY = "loop-channel-order";
// Per-parent thread/worktree order, keyed by parent channel id. Client-side
// only (localStorage), mirroring the channel-order approach — the backend
// returns threads alphabetically and stays the source of truth for membership.
const THREAD_ORDER_STORAGE_KEY = "loop-thread-order";

function loadOrder(): string[] {
  return storageGetJSON<string[]>(ORDER_STORAGE_KEY) ?? [];
}

function saveOrder(ids: string[]) {
  storageSetJSON(ORDER_STORAGE_KEY, ids);
}

function loadThreadOrder(): Record<string, string[]> {
  return storageGetJSON<Record<string, string[]>>(THREAD_ORDER_STORAGE_KEY) ?? {};
}

function saveThreadOrder(order: Record<string, string[]>) {
  storageSetJSON(THREAD_ORDER_STORAGE_KEY, order);
}

function sortByOrder(channels: Channel[], order: string[]): Channel[] {
  if (order.length === 0) return channels;
  const indexMap = new Map(order.map((id, i) => [id, i]));
  return [...channels].sort((a, b) => {
    const ai = indexMap.get(a.id) ?? Infinity;
    const bi = indexMap.get(b.id) ?? Infinity;
    return ai - bi;
  });
}

interface ContextMenuState {
  x: number;
  y: number;
  items: MenuItem[];
}

interface SidebarProps {
  channels: Channel[];
  selectedId: string | null;
  collapsed: boolean;
  onSelect: (id: string) => void;
  onCreateChannel: (name: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
  onCreateWorktree?: (channelId: string, branch: string) => void;
  onDeleteThread: (threadId: string) => void;
  onRenameThread?: (threadId: string, newName: string, isWorktree: boolean) => void;
  onSetLocked?: (channelId: string, locked: boolean) => void;
  onDeleteBatch?: (ids: string[]) => void;
  onOpenDirectory?: (dirPath: string) => void;
  onOpenSettings?: () => void;
  onOpenConfig?: (dirPath: string) => void;
  onOpenReadme?: () => void;
  onOpenContainers?: () => void;
  onOpenTasks?: () => void;
  onOpenWorkflows?: () => void;
  onOpenShares?: () => void;
  shareCount?: number;
  updateStatus?: UpdateStatus | null;
  onDownloadUpdate?: () => void;
  onInstallUpdate?: () => void;
  /** Real-time running status from the app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, string>>;
  /** Channels with unread agent completions. */
  unreadIdsRef?: React.RefObject<Set<string>>;
  /** Channels with at least one pending gate approval (chat or terminal). */
  gateChannelIdsRef?: React.RefObject<Set<string>>;
  /** Channels with a loaded review session (status=ready). */
  reviewChannelIdsRef?: React.RefObject<Set<string>>;
  /** Channels parked on an AskUserQuestion card. */
  askUserChannelIdsRef?: React.RefObject<Set<string>>;
  unreadCount?: number;
  onMarkAllRead?: () => void;
  imageBuildStatus?: ImageBuildStatusData | null;
  imageUpdateAvailable?: ImageUpdateAvailableData | null;
  onRebuildImage?: () => void;
  onToggleSidebar?: () => void;
}

export function Sidebar({
  channels,
  selectedId,
  collapsed,
  onSelect,
  onCreateChannel,
  onCreateThread,
  onCreateWorktree,
  onDeleteThread,
  onRenameThread,
  onSetLocked,
  onDeleteBatch,
  onOpenDirectory,
  onOpenSettings,
  onOpenConfig,
  onOpenReadme,
  onOpenContainers,
  onOpenTasks,
  onOpenWorkflows,
  onOpenShares,
  shareCount,
  updateStatus,
  onDownloadUpdate,
  onInstallUpdate,
  isRunningMapRef,
  unreadIdsRef,
  gateChannelIdsRef,
  reviewChannelIdsRef,
  askUserChannelIdsRef,
  unreadCount,
  onMarkAllRead,
  imageBuildStatus,
  imageUpdateAvailable,
  onRebuildImage,
  onToggleSidebar,
}: SidebarProps) {
  const { colors, fontSizes } = useTheme();
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [resizing, setResizing] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [renaming, setRenaming] = useState<Channel | null>(null);
  const [channelOrder, setChannelOrder] = useState<string[]>(loadOrder);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [threadOrder, setThreadOrder] = useState<Record<string, string[]>>(loadThreadOrder);
  const [threadDragOverId, setThreadDragOverId] = useState<string | null>(null);
  const draggedThreadRef = useRef<{ id: string; parentId: string } | null>(null);
  const [creatingChannel, setCreatingChannel] = useState(false);
  const [newChannelName, setNewChannelName] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [selectMode, setSelectMode] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const newChannelInputRef = useRef<HTMLInputElement>(null);
  const draggedIdRef = useRef<string | null>(null);
  const sidebarRef = useRef<HTMLDivElement>(null);

  const toggleSelected = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }, []);

  const handleBatchDelete = useCallback(() => {
    if (selected.size === 0) return;
    onDeleteBatch?.([...selected]);
    setSelected(new Set());
    setSelectMode(false);
  }, [selected, onDeleteBatch]);

  const handleDragStart = useCallback((channelId: string) => {
    draggedIdRef.current = channelId;
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent, channelId: string) => {
    // Only accept channel-on-channel drops. When a thread is being dragged
    // (draggedThreadRef set, draggedIdRef null), skip preventDefault so the
    // channel row is a non-target and the cursor shows "no-drop".
    if (!draggedIdRef.current) return;
    e.preventDefault();
    setDragOverId(channelId);
  }, []);

  const handleDrop = useCallback((e: React.DragEvent, targetId: string) => {
    e.preventDefault();
    setDragOverId(null);
    const sourceId = draggedIdRef.current;
    draggedIdRef.current = null;
    if (!sourceId || sourceId === targetId) return;

    const topLevel = channels.filter((c) => !c.parent_id && (c.name || c.dir_path));
    const currentIds = sortByOrder(topLevel, channelOrder).map((c) => c.id);
    const fromIdx = currentIds.indexOf(sourceId);
    const toIdx = currentIds.indexOf(targetId);
    if (fromIdx === -1 || toIdx === -1) return;

    currentIds.splice(fromIdx, 1);
    currentIds.splice(toIdx, 0, sourceId);
    setChannelOrder(currentIds);
    saveOrder(currentIds);
  }, [channels, channelOrder]);

  const handleDragEnd = useCallback(() => {
    draggedIdRef.current = null;
    setDragOverId(null);
  }, []);

  // Thread/worktree drag-reorder within a single parent channel. Reorders are
  // persisted per-parent in localStorage; cross-parent drops are ignored.
  const handleThreadDragStart = useCallback((threadId: string, parentId: string) => {
    draggedThreadRef.current = { id: threadId, parentId };
  }, []);

  const handleThreadDragOver = useCallback((e: React.DragEvent, threadId: string, parentId: string) => {
    // Only show a drop indicator on a sibling under the SAME parent. A channel
    // drag (no thread ref) or hovering a thread in another channel is a
    // non-target, so skip preventDefault and the cursor shows no-drop.
    const src = draggedThreadRef.current;
    if (!src || src.parentId !== parentId) return;
    e.preventDefault();
    e.stopPropagation();
    setThreadDragOverId(threadId);
  }, []);

  const handleThreadDrop = useCallback((e: React.DragEvent, targetId: string, parentId: string) => {
    // Not a thread drag (e.g. a channel dropped over a thread row): let the
    // event bubble to the channel row's drop handler instead of swallowing it.
    if (!draggedThreadRef.current) return;
    e.preventDefault();
    e.stopPropagation();
    setThreadDragOverId(null);
    const src = draggedThreadRef.current;
    draggedThreadRef.current = null;
    // Only reorder among siblings of the same parent — no cross-parent moves.
    if (src.id === targetId || src.parentId !== parentId) return;

    const siblings = channels.filter((c) => c.parent_id === parentId);
    const ids = sortByOrder(siblings, threadOrder[parentId] ?? []).map((c) => c.id);
    const from = ids.indexOf(src.id);
    const to = ids.indexOf(targetId);
    if (from === -1 || to === -1) return;
    ids.splice(from, 1);
    ids.splice(to, 0, src.id);
    setThreadOrder((prev) => {
      const next = { ...prev, [parentId]: ids };
      saveThreadOrder(next);
      return next;
    });
  }, [channels, threadOrder]);

  const handleThreadDragEnd = useCallback(() => {
    draggedThreadRef.current = null;
    setThreadDragOverId(null);
  }, []);

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, channel: Channel) => {
      e.preventDefault();
      const isDm = channel.name === "dm" && !channel.parent_id;
      const isThread = !!channel.parent_id;
      const items: MenuItem[] = [
        {
          label: "Copy Link",
          onClick: () => navigator.clipboard.writeText(`loop://channel/${channel.id}`),
        },
        {
          label: isThread ? "Copy Thread ID" : "Copy Channel ID",
          onClick: () => navigator.clipboard.writeText(channel.id),
        },
      ];
      // Rename is offered for threads (incl. worktree threads) only — never the
      // DM, and not while locked (mirrors Delete: unlock first to confirm intent).
      if (isThread && !isDm && !channel.locked && onRenameThread) {
        items.push({
          label: channel.worktree ? "Rename Worktree" : "Rename Thread",
          separator: true,
          onClick: () => setRenaming(channel),
        });
      }
      if (!isDm && onSetLocked) {
        items.push({
          label: channel.locked ? "Unlock" : "Lock",
          separator: !(isThread && !channel.locked && onRenameThread),
          onClick: () => onSetLocked(channel.id, !channel.locked),
        });
      }
      // Hide delete when locked: the operator must Unlock first to confirm
      // intent. This prevents accidental loss of work parked in long-running
      // threads.
      if (!isDm && !channel.locked) {
        items.push({
          label: isThread ? "Delete Thread" : "Delete Channel",
          danger: true,
          separator: !onSetLocked,
          onClick: () => onDeleteThread(channel.id),
        });
      }
      setContextMenu({ x: e.clientX, y: e.clientY, items });
    },
    [onDeleteThread, onRenameThread, onSetLocked],
  );

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);

      const startX = e.clientX;
      const startWidth = width;

      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(
          maxWidth,
          Math.max(MIN_WIDTH, startWidth + ev.clientX - startX),
        );
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  const query = searchQuery.toLowerCase();
  const threadsByParent = channels.reduce<Record<string, Channel[]>>(
    (acc, c) => {
      if (c.parent_id) {
        (acc[c.parent_id] ??= []).push(c);
      }
      return acc;
    },
    {},
  );
  // Apply the per-parent drag-reorder order; parents with no saved order keep
  // the backend's alphabetical order.
  for (const parentId of Object.keys(threadsByParent)) {
    const order = threadOrder[parentId];
    const list = threadsByParent[parentId];
    if (list && order && order.length > 0) {
      threadsByParent[parentId] = sortByOrder(list, order);
    }
  }

  // Separate DM channel (pinned at top) from regular channels.
  const dmChannel = channels.find((c) => c.name === "dm" && !c.parent_id);
  const allTopLevel = sortByOrder(
    channels.filter((c) => !c.parent_id && (c.name || c.dir_path) && !(c.name === "dm" && !c.parent_id)),
    channelOrder,
  );

  // Check if a thread or any of its sub-threads match the search query.
  const threadTreeMatches = (t: Channel): boolean => {
    if (t.name.toLowerCase().includes(query)) return true;
    const subs = threadsByParent[t.id] ?? [];
    return subs.some(threadTreeMatches);
  };

  // When searching, show channels that match or have matching threads/sub-threads.
  const topLevel = query
    ? allTopLevel.filter((c) => {
        const nameMatch = c.name.toLowerCase().includes(query);
        const threads = threadsByParent[c.id] ?? [];
        const threadMatch = threads.some(threadTreeMatches);
        return nameMatch || threadMatch;
      })
    : allTopLevel;

  // Filter threads when searching.
  const getFilteredThreads = (parentId: string): Channel[] => {
    const threads = threadsByParent[parentId] ?? [];
    if (!query) return threads;
    const parentMatches = allTopLevel.find((c) => c.id === parentId)?.name.toLowerCase().includes(query);
    if (parentMatches) return threads;
    return threads.filter(threadTreeMatches);
  };

  if (collapsed) {
    return null;
  }

  return (
    <div
      data-testid="sidebar"
      ref={sidebarRef}
      style={{
        width,
        minWidth: MIN_WIDTH,
        flexShrink: 0,
        backgroundColor: colors.sidebarNav,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        zoom: fontSizes.sidebar / 12,
        // Prevent text selection while dragging
        userSelect: resizing ? "none" : undefined,
        borderRadius: colors.islandRadius,
        boxShadow: colors.islandShadow,
        border: colors.islandBorder,
        ...(!colors.islandRadius && { borderRight: `1px solid ${colors.border}` }),
      }}
    >
      {/* Drag region over traffic lights area */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          WebkitAppRegion: "drag",
          position: "relative",
        }}
      >
        {onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Collapse sidebar"
            style={{
              position: "absolute",
              right: 4,
              top: 10,
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
              <polyline points="15,9 12,12 15,15" />
            </svg>
          </button>
        )}
      </div>
      <SidebarHeader
        searchQuery={searchQuery}
        onSearchQueryChange={setSearchQuery}
        selectMode={selectMode}
        selectedCount={selected.size}
        onBatchDelete={handleBatchDelete}
        onEnterSelectMode={() => { setSelectMode(true); setSelected(new Set()); }}
        onCancelSelectMode={() => { setSelectMode(false); setSelected(new Set()); }}
        unreadCount={unreadCount}
        onMarkAllRead={onMarkAllRead}
        onNewProject={() => {
          setCreatingChannel(true);
          setNewChannelName("");
          setTimeout(() => newChannelInputRef.current?.focus(), 0);
        }}
        onOpenDirectory={onOpenDirectory}
        creatingChannel={creatingChannel}
        newChannelName={newChannelName}
        onNewChannelNameChange={setNewChannelName}
        onCreateChannel={(name) => {
          onCreateChannel(name);
          setCreatingChannel(false);
          setNewChannelName("");
        }}
        onCancelCreateChannel={() => {
          setCreatingChannel(false);
          setNewChannelName("");
        }}
        newChannelInputRef={newChannelInputRef}
      />
      <div style={{ flex: 1, overflowY: "auto", overflowX: "hidden", minHeight: 0 }}>
        <ChannelList
          dmChannel={dmChannel}
          topLevel={topLevel}
          selectedId={selectedId}
          onSelect={onSelect}
          onCreateThread={onCreateThread}
          onCreateWorktree={onCreateWorktree}
          threadReorder={{
            onDragStart: handleThreadDragStart,
            onDragOver: handleThreadDragOver,
            onDrop: handleThreadDrop,
            onDragEnd: handleThreadDragEnd,
            dragOverId: threadDragOverId,
          }}
          onOpenConfig={onOpenConfig}
          onContextMenu={handleContextMenu}
          onDragStart={handleDragStart}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onDragEnd={handleDragEnd}
          dragOverId={dragOverId}
          getFilteredThreads={getFilteredThreads}
          threadsByParent={threadsByParent}
          selectMode={selectMode}
          checkedIds={selected}
          onToggleCheck={toggleSelected}
          isRunningMapRef={isRunningMapRef}
          unreadIdsRef={unreadIdsRef}
          gateChannelIdsRef={gateChannelIdsRef}
          reviewChannelIdsRef={reviewChannelIdsRef}
          askUserChannelIdsRef={askUserChannelIdsRef}
        />
      </div>

      <SidebarFooter
        updateStatus={updateStatus}
        onDownloadUpdate={onDownloadUpdate}
        onInstallUpdate={onInstallUpdate}
        imageBuildStatus={imageBuildStatus}
        imageUpdateAvailable={imageUpdateAvailable}
        onRebuildImage={onRebuildImage}
        onOpenSettings={onOpenSettings}
        onOpenTasks={onOpenTasks}
        onOpenWorkflows={onOpenWorkflows}
        onOpenShares={onOpenShares}
        shareCount={shareCount}
        onOpenContainers={onOpenContainers}
        onOpenReadme={onOpenReadme}
      />

      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={contextMenu.items}
          onClose={() => setContextMenu(null)}
        />
      )}

      {renaming && (
        <RenameThreadDialog
          currentName={renaming.name}
          isWorktree={!!renaming.worktree}
          onCancel={() => setRenaming(null)}
          onSubmit={(newName) => {
            onRenameThread?.(renaming.id, newName, !!renaming.worktree);
            setRenaming(null);
          }}
        />
      )}

      {/* Resize handle */}
      <div
        onMouseDown={handleMouseDown}
        style={{
          position: "absolute",
          top: 0,
          right: -colors.islandGap,
          width: colors.islandGap + 4,
          height: "100%",
          cursor: "col-resize",
          backgroundColor: "transparent",
        }}
        onMouseEnter={() => {}}
        onMouseLeave={() => {}}
      />

      {/* Inline keyframes for spinner */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  );
}
