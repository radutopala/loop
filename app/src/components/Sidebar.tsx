import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel, ImageBuildStatusData, ImageUpdateAvailableData, UpdateStatus } from "../types";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import { ChannelItem } from "./ChannelItem";
import { ContextMenu } from "./ContextMenu";
import type { MenuItem } from "./ContextMenu";

const MIN_WIDTH = 180;
const MAX_WIDTH_PERCENT = 0.25;
const DEFAULT_WIDTH = 280;
const ORDER_STORAGE_KEY = "loop-channel-order";

function loadOrder(): string[] {
  try {
    const stored = localStorage.getItem(ORDER_STORAGE_KEY);
    if (stored) return JSON.parse(stored);
  } catch { /* ignore */ }
  return [];
}

function saveOrder(ids: string[]) {
  try {
    localStorage.setItem(ORDER_STORAGE_KEY, JSON.stringify(ids));
  } catch { /* ignore */ }
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

function NewMenu({ onNewProject, onOpenDirectory, onClose }: {
  onNewProject: () => void;
  onOpenDirectory?: () => void;
  onClose: () => void;
}) {
  const { colors } = useTheme();
  const ref = useRef<HTMLDivElement>(null);

  const dropdownItemStyle: React.CSSProperties = {
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

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (e.button !== 0) return;
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose();
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [onClose]);

  return (
    <div
      ref={ref}
      style={{
        position: "absolute",
        top: 74,
        right: 8,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        padding: 4,
        zIndex: 100,
        minWidth: 150,
        boxShadow: `0 4px 12px ${colors.shadow}`,
        fontFamily: fonts.sans,
      }}
    >
      <button
        onClick={onNewProject}
        style={dropdownItemStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
      >
        New project
      </button>
      {onOpenDirectory && (
        <button
          onClick={onOpenDirectory}
          style={dropdownItemStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
        >
          Open directory...
        </button>
      )}
    </div>
  );
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
  onDeleteBatch?: (ids: string[]) => void;
  onOpenDirectory?: (dirPath: string) => void;
  onOpenSettings?: () => void;
  onOpenConfig?: (dirPath: string) => void;
  onOpenReadme?: () => void;
  onOpenContainers?: () => void;
  onOpenTasks?: () => void;
  updateStatus?: UpdateStatus | null;
  onDownloadUpdate?: () => void;
  onInstallUpdate?: () => void;
  /** Real-time running status from the app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, boolean>>;
  /** Channels with unread agent completions. */
  unreadIdsRef?: React.RefObject<Set<string>>;
  unreadCount?: number;
  onMarkAllRead?: () => void;
  imageBuildStatus?: ImageBuildStatusData | null;
  imageUpdateAvailable?: ImageUpdateAvailableData | null;
  onRebuildImage?: () => void;
}

export function Sidebar({
  channels,
  selectedId,
  collapsed,
  onSelect,
  onCreateChannel,
  onCreateThread,
  onDeleteThread,
  onDeleteBatch,
  onOpenDirectory,
  onOpenSettings,
  onOpenConfig,
  onOpenReadme,
  onOpenContainers,
  onOpenTasks,
  updateStatus,
  onDownloadUpdate,
  onInstallUpdate,
  isRunningMapRef,
  unreadIdsRef,
  unreadCount,
  onMarkAllRead,
  imageBuildStatus,
  imageUpdateAvailable,
  onRebuildImage,
}: SidebarProps) {
  const { colors, fontSizes } = useTheme();
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [resizing, setResizing] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [channelOrder, setChannelOrder] = useState<string[]>(loadOrder);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [creatingChannel, setCreatingChannel] = useState(false);
  const [newChannelName, setNewChannelName] = useState("");
  const [newMenuOpen, setNewMenuOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [selectMode, setSelectMode] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const newChannelInputRef = useRef<HTMLInputElement>(null);
  const draggedIdRef = useRef<string | null>(null);
  const sidebarRef = useRef<HTMLDivElement>(null);

  const sidebarBtnStyle: React.CSSProperties = {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "3px 8px",
    fontSize: 12,
    lineHeight: 1,
    borderRadius: 4,
  };

  const footerBtnStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 8,
    width: "100%",
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "6px 8px",
    fontSize: 12,
    borderRadius: 6,
    fontFamily: "inherit",
  };

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
      if (!isDm) {
        items.push({
          label: isThread ? "Delete Thread" : "Delete Channel",
          danger: true,
          separator: true,
          onClick: () => onDeleteThread(channel.id),
        });
      }
      setContextMenu({ x: e.clientX, y: e.clientY, items });
    },
    [onDeleteThread],
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
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      />
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 35,
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Channels
        </span>
        <div style={{ display: "flex", alignItems: "center", gap: 2 }}>
          {selectMode ? (
            <>
              {selected.size > 0 && (
                <button
                  onClick={handleBatchDelete}
                  title={`Delete ${selected.size} selected`}
                  style={sidebarBtnStyle}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.error; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
                >
                  Delete ({selected.size})
                </button>
              )}
              <button
                onClick={() => { setSelectMode(false); setSelected(new Set()); }}
                title="Cancel selection"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              {(unreadCount ?? 0) > 0 && (
                <button
                  onClick={() => onMarkAllRead?.()}
                  title="Mark all as read"
                  style={sidebarBtnStyle}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
                >
                  Mark read
                </button>
              )}
              <button
                onClick={() => { setSelectMode(true); setSelected(new Set()); }}
                title="Select channels to delete"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                Select
              </button>
              <button
                onClick={() => setNewMenuOpen((v) => !v)}
                title="New channel"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                + new
              </button>
            </>
          )}
        </div>
      </div>
      {newMenuOpen && (
        <NewMenu
          onNewProject={() => {
            setNewMenuOpen(false);
            setCreatingChannel(true);
            setNewChannelName("");
            setTimeout(() => newChannelInputRef.current?.focus(), 0);
          }}
          onOpenDirectory={onOpenDirectory ? async () => {
            setNewMenuOpen(false);
            const dirPath = await window.loopAPI?.showOpenDirectoryDialog?.();
            if (dirPath) onOpenDirectory(dirPath);
          } : undefined}
          onClose={() => setNewMenuOpen(false)}
        />
      )}
      <div style={{ padding: "6px 12px 4px" }}>
        <div style={{ position: "relative" }}>
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke={colors.textDim}
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ position: "absolute", left: 7, top: "50%", transform: "translateY(-50%)", pointerEvents: "none" }}
          >
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Escape") setSearchQuery(""); }}
            placeholder="Search..."
            style={{
              width: "100%",
              background: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              color: colors.textLight,
              fontSize: 12,
              padding: "4px 8px 4px 24px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        </div>
      </div>
      {creatingChannel && (
        <div style={{ padding: "4px 12px 8px" }}>
          <input
            ref={newChannelInputRef}
            value={newChannelName}
            onChange={(e) => setNewChannelName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && newChannelName.trim()) {
                onCreateChannel(newChannelName.trim());
                setCreatingChannel(false);
                setNewChannelName("");
              } else if (e.key === "Escape") {
                setCreatingChannel(false);
                setNewChannelName("");
              }
            }}
            onBlur={() => {
              setCreatingChannel(false);
              setNewChannelName("");
            }}
            placeholder="Channel name..."
            style={{
              width: "100%",
              background: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              color: colors.textLight,
              fontSize: 13,
              padding: "4px 8px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        </div>
      )}
      {dmChannel && (
        <ChannelItem
          key={dmChannel.id}
          channel={dmChannel}
          threads={getFilteredThreads(dmChannel.id)}
          threadsByParent={threadsByParent}
          selected={selectedId === dmChannel.id}
          selectedId={selectedId}
          onSelect={onSelect}
          onCreateThread={onCreateThread}
          onOpenConfig={onOpenConfig}
          onContextMenu={handleContextMenu}
          onDragStart={handleDragStart}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onDragEnd={handleDragEnd}
          isDragOver={dragOverId === dmChannel.id}
          pinned
          selectMode={selectMode}
          checkedIds={selected}
          onToggleCheck={toggleSelected}
          isRunningMapRef={isRunningMapRef}
          unreadIdsRef={unreadIdsRef}
        />
      )}
      {topLevel.map((channel) => (
        <ChannelItem
          key={channel.id}
          channel={channel}
          threads={getFilteredThreads(channel.id)}
          threadsByParent={threadsByParent}
          selected={selectedId === channel.id}
          selectedId={selectedId}
          onSelect={onSelect}
          onCreateThread={onCreateThread}
          onOpenConfig={onOpenConfig}
          onContextMenu={handleContextMenu}
          onDragStart={handleDragStart}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onDragEnd={handleDragEnd}
          isDragOver={dragOverId === channel.id}
          selectMode={selectMode}
          checkedIds={selected}
          onToggleCheck={toggleSelected}
          isRunningMapRef={isRunningMapRef}
          unreadIdsRef={unreadIdsRef}
        />
      ))}

      {/* Spacer to push footer to bottom */}
      <div style={{ flex: 1 }} />

      {/* Footer: Update + Settings + README */}
      <div style={{ padding: "8px 12px", borderTop: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 2 }}>
        {updateStatus?.available && (
          <button
            onClick={updateStatus.downloaded ? onInstallUpdate : updateStatus.downloading ? undefined : onDownloadUpdate}
            disabled={updateStatus.downloading}
            style={{
              ...footerBtnStyle,
              color: updateStatus.downloaded ? colors.active : updateStatus.downloading ? colors.textDim : colors.active,
              cursor: updateStatus.downloading ? "default" : "pointer",
            }}
            onMouseEnter={(e) => { if (!updateStatus.downloading) { e.currentTarget.style.backgroundColor = colors.hoverBg; } }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
            {updateStatus.downloaded
              ? "Restart to update"
              : updateStatus.error
                ? "Update failed — click to retry"
                : updateStatus.downloading
                  ? "Downloading..."
                  : `Update available${updateStatus.version ? ` v${updateStatus.version}` : ""}`}
          </button>
        )}
        {imageBuildStatus?.state === "building" && (
          <div
            style={{
              ...footerBtnStyle,
              color: colors.warning,
              cursor: "default",
            }}
          >
            <svg
              width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
              style={{ animation: "spin 1s linear infinite" }}
            >
              <path d="M21 12a9 9 0 1 1-3-6.7" />
              <polyline points="21,3 21,9 15,9" />
            </svg>
            Building image...
          </div>
        )}
        {imageBuildStatus?.state === "failed" && (
          <div
            style={{
              ...footerBtnStyle,
              color: colors.error,
              cursor: "default",
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="15" y1="9" x2="9" y2="15" />
              <line x1="9" y1="9" x2="15" y2="15" />
            </svg>
            Image build failed
          </div>
        )}
        {imageUpdateAvailable && imageBuildStatus?.state !== "building" && imageBuildStatus?.state !== "failed" && (
          <button
            onClick={onRebuildImage}
            style={{
              ...footerBtnStyle,
              color: colors.active,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
            onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
            Claude update available
          </button>
        )}
        <button
          onClick={onOpenSettings}
          style={footerBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          Settings
        </button>
        <button
          onClick={onOpenTasks}
          style={footerBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" />
            <polyline points="12 6 12 12 16 14" />
          </svg>
          Tasks
        </button>
        <button
          onClick={onOpenContainers}
          style={footerBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
            <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
            <line x1="12" y1="22.08" x2="12" y2="12" />
          </svg>
          Containers
        </button>
        <button
          onClick={onOpenReadme}
          style={footerBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
            <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
          </svg>
          README
        </button>
      </div>

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
