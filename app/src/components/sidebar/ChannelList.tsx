import type { Channel } from "../../types";
import { ChannelItem } from "./ChannelItem";
import type { ThreadReorder } from "./ThreadItem";

interface ChannelListProps {
  dmChannel: Channel | undefined;
  topLevel: Channel[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
  onCreateWorktree?: (channelId: string, branch: string) => void;
  threadReorder?: ThreadReorder;
  onOpenConfig?: (dirPath: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  onDragStart: (channelId: string) => void;
  onDragOver: (e: React.DragEvent, channelId: string) => void;
  onDrop: (e: React.DragEvent, channelId: string) => void;
  onDragEnd: () => void;
  dragOverId: string | null;
  getFilteredThreads: (parentId: string) => Channel[];
  threadsByParent: Record<string, Channel[]>;
  selectMode: boolean;
  checkedIds: Set<string>;
  onToggleCheck: (id: string) => void;
  isRunningMapRef?: React.RefObject<Map<string, string>>;
  unreadIdsRef?: React.RefObject<Set<string>>;
  gateChannelIdsRef?: React.RefObject<Set<string>>;
  reviewChannelIdsRef?: React.RefObject<Set<string>>;
  askUserChannelIdsRef?: React.RefObject<Set<string>>;
}

export function ChannelList({
  dmChannel,
  topLevel,
  selectedId,
  onSelect,
  onCreateThread,
  onCreateWorktree,
  threadReorder,
  onOpenConfig,
  onContextMenu,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  dragOverId,
  getFilteredThreads,
  threadsByParent,
  selectMode,
  checkedIds,
  onToggleCheck,
  isRunningMapRef,
  unreadIdsRef,
  gateChannelIdsRef,
  reviewChannelIdsRef,
  askUserChannelIdsRef,
}: ChannelListProps) {
  return (
    <>
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
          onCreateWorktree={onCreateWorktree}
          threadReorder={threadReorder}
          onOpenConfig={onOpenConfig}
          onContextMenu={onContextMenu}
          onDragStart={onDragStart}
          onDragOver={onDragOver}
          onDrop={onDrop}
          onDragEnd={onDragEnd}
          isDragOver={dragOverId === dmChannel.id}
          pinned
          selectMode={selectMode}
          checkedIds={checkedIds}
          onToggleCheck={onToggleCheck}
          isRunningMapRef={isRunningMapRef}
          unreadIdsRef={unreadIdsRef}
          gateChannelIdsRef={gateChannelIdsRef}
          reviewChannelIdsRef={reviewChannelIdsRef}
          askUserChannelIdsRef={askUserChannelIdsRef}
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
          onCreateWorktree={onCreateWorktree}
          threadReorder={threadReorder}
          onOpenConfig={onOpenConfig}
          onContextMenu={onContextMenu}
          onDragStart={onDragStart}
          onDragOver={onDragOver}
          onDrop={onDrop}
          onDragEnd={onDragEnd}
          isDragOver={dragOverId === channel.id}
          selectMode={selectMode}
          checkedIds={checkedIds}
          onToggleCheck={onToggleCheck}
          isRunningMapRef={isRunningMapRef}
          unreadIdsRef={unreadIdsRef}
          gateChannelIdsRef={gateChannelIdsRef}
          reviewChannelIdsRef={reviewChannelIdsRef}
          askUserChannelIdsRef={askUserChannelIdsRef}
        />
      ))}
    </>
  );
}
