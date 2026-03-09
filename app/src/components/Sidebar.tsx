import { useCallback, useRef, useState } from "react";
import type { Channel } from "../types";
import { colors } from "../theme";
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
}

export function Sidebar({
  channels,
  selectedId,
  collapsed,
  onSelect,
  onCreateChannel,
  onCreateThread,
  onDeleteThread,
}: SidebarProps) {
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [resizing, setResizing] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [channelOrder, setChannelOrder] = useState<string[]>(loadOrder);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [creatingChannel, setCreatingChannel] = useState(false);
  const [newChannelName, setNewChannelName] = useState("");
  const newChannelInputRef = useRef<HTMLInputElement>(null);
  const draggedIdRef = useRef<string | null>(null);
  const sidebarRef = useRef<HTMLDivElement>(null);

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
      setContextMenu({
        x: e.clientX,
        y: e.clientY,
        items: [
          {
            label: "Copy Link",
            onClick: () => navigator.clipboard.writeText(`loop://channel/${channel.id}`),
          },
          {
            label: "Copy Channel ID",
            onClick: () => navigator.clipboard.writeText(channel.id),
          },
          {
            label: "Delete Channel",
            danger: true,
            separator: true,
            onClick: () => onDeleteThread(channel.id),
          },
        ],
      });
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

  const topLevel = sortByOrder(
    channels.filter((c) => !c.parent_id && (c.name || c.dir_path)),
    channelOrder,
  );
  const threadsByParent = channels.reduce<Record<string, Channel[]>>(
    (acc, c) => {
      if (c.parent_id) {
        (acc[c.parent_id] ??= []).push(c);
      }
      return acc;
    },
    {},
  );

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
        backgroundColor: colors.sidebar,
        display: "flex",
        flexDirection: "column",
        overflow: "auto",
        position: "relative",
        // Prevent text selection while dragging
        userSelect: resizing ? "none" : undefined,
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
          padding: "8px 12px 8px",
        }}
      >
        <span
          style={{
            fontSize: 11,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Channels
        </span>
        <button
          onClick={() => {
            setCreatingChannel(true);
            setNewChannelName("");
            setTimeout(() => newChannelInputRef.current?.focus(), 0);
          }}
          title="New channel"
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
            cursor: "pointer",
            padding: "3px 8px",
            fontSize: 12,
            lineHeight: 1,
            borderRadius: 4,
          }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          + new
        </button>
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
      {topLevel.map((channel) => (
        <ChannelItem
          key={channel.id}
          channel={channel}
          threads={threadsByParent[channel.id] ?? []}
          selected={selectedId === channel.id}
          selectedId={selectedId}
          onSelect={onSelect}
          onCreateThread={onCreateThread}
          onContextMenu={handleContextMenu}
          onDragStart={handleDragStart}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          onDragEnd={handleDragEnd}
          isDragOver={dragOverId === channel.id}
        />
      ))}

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
          right: 0,
          width: 4,
          height: "100%",
          cursor: "col-resize",
          backgroundColor: resizing ? colors.textDim : "transparent",
          borderRight: `1px solid ${colors.border}`,
        }}
        onMouseEnter={(e) => {
          (e.currentTarget as HTMLDivElement).style.backgroundColor =
            colors.textDim;
        }}
        onMouseLeave={(e) => {
          if (!resizing) {
            (e.currentTarget as HTMLDivElement).style.backgroundColor =
              "transparent";
          }
        }}
      />
    </div>
  );
}
