import { useCallback, useRef, useState } from "react";
import type { Channel, ImageBuildStatusData, ImageUpdateAvailableData, UpdateStatus } from "../../types";
import { useTheme } from "../../ThemeContext";
import { ContextMenu } from "../shared/ContextMenu";
import type { MenuItem } from "../shared/ContextMenu";
import { SidebarHeader } from "./SidebarHeader";
import { ChannelList } from "./ChannelList";
import { SidebarFooter } from "./SidebarFooter";
import { storageGetJSON, storageSetJSON } from "../../utils/storage";

const MIN_WIDTH = 180;
const MAX_WIDTH_PERCENT = 0.25;
const DEFAULT_WIDTH = 280;
const ORDER_STORAGE_KEY = "loop-channel-order";

function loadOrder(): string[] {
  return storageGetJSON<string[]>(ORDER_STORAGE_KEY) ?? [];
}

function saveOrder(ids: string[]) {
  storageSetJSON(ORDER_STORAGE_KEY, ids);
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
  onDeleteThread: (threadId: string) => void;
  onSetLocked?: (channelId: string, locked: boolean) => void;
  onDeleteBatch?: (ids: string[]) => void;
  onOpenDirectory?: (dirPath: string) => void;
  onOpenSettings?: () => void;
  onOpenConfig?: (dirPath: string) => void;
  onOpenReadme?: () => void;
  onOpenContainers?: () => void;
  onOpenTasks?: () => void;
  onOpenWorkflows?: () => void;
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
  onDeleteThread,
  onSetLocked,
  onDeleteBatch,
  onOpenDirectory,
  onOpenSettings,
  onOpenConfig,
  onOpenReadme,
  onOpenContainers,
  onOpenTasks,
  onOpenWorkflows,
  updateStatus,
  onDownloadUpdate,
  onInstallUpdate,
  isRunningMapRef,
  unreadIdsRef,
  gateChannelIdsRef,
  reviewChannelIdsRef,
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
  const [channelOrder, setChannelOrder] = useState<string[]>(loadOrder);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
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
      if (!isDm && onSetLocked) {
        items.push({
          label: channel.locked ? "Unlock" : "Lock",
          separator: true,
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
    [onDeleteThread, onSetLocked],
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
        overflowX: "hidden",
        overflowY: "auto",
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
      <ChannelList
        dmChannel={dmChannel}
        topLevel={topLevel}
        selectedId={selectedId}
        onSelect={onSelect}
        onCreateThread={onCreateThread}
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
      />

      {/* Spacer to push footer to bottom */}
      <div style={{ flex: 1 }} />

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
