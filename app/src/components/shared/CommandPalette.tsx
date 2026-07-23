import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type MemoryFileInfo, type SearchMessageResult, searchMemoryFiles, searchMessages } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";

interface CommandPaletteProps {
  channels: Channel[];
  selectedChannelId?: string | null;
  open: boolean;
  onClose: () => void;
  onSelect: (id: string) => void;
  onSelectMessage?: (channelId: string, messageId: number) => void;
  onSelectMemoryFile?: (filePath: string) => void;
}

interface PaletteItem {
  id: string;
  label: string;
  detail: string;
  kind: "channel" | "thread" | "message" | "memory";
  channelId?: string;
  messageId?: number;
  filePath?: string;
}

function fuzzyMatch(query: string, text: string): boolean {
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  let qi = 0;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  return qi === q.length;
}

function truncate(text: string, max: number): string {
  const oneLine = text.replace(/\n/g, " ").trim();
  return oneLine.length > max ? oneLine.slice(0, max) + "…" : oneLine;
}

export function CommandPalette({ channels, selectedChannelId, open, onClose, onSelect, onSelectMessage, onSelectMemoryFile }: CommandPaletteProps) {
  const { colors } = useTheme();
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [messageResults, setMessageResults] = useState<SearchMessageResult[]>([]);
  const [memoryFiles, setMemoryFiles] = useState<MemoryFileInfo[]>([]);
  const [searching, setSearching] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Build channel name lookup for message results.
  const channelNameMap = useMemo(() => {
    const map = new Map<string, string>();
    for (const ch of channels) {
      map.set(ch.id, ch.name || ch.dir_path);
    }
    return map;
  }, [channels]);

  // Build flat list of items: channels + threads with parent context.
  const allItems = useMemo((): PaletteItem[] => {
    const items: PaletteItem[] = [];
    const parentMap = new Map(channels.filter((c) => !c.parent_id).map((c) => [c.id, c]));

    // Channels first.
    for (const ch of channels) {
      if (ch.parent_id) continue;
      items.push({
        id: ch.id,
        label: ch.name || ch.dir_path,
        detail: ch.branch ? `${ch.dir_path} | ${ch.branch}` : ch.dir_path,
        kind: "channel",
      });
    }

    // Then threads.
    for (const ch of channels) {
      if (!ch.parent_id) continue;
      const parent = parentMap.get(ch.parent_id);
      items.push({
        id: ch.id,
        label: ch.name,
        detail: parent ? `${parent.name} › thread` : "thread",
        kind: "thread",
      });
    }

    return items;
  }, [channels]);

  const filteredChannels = useMemo(() => {
    if (!query.trim()) return allItems;
    return allItems.filter((item) => fuzzyMatch(query, item.label) || fuzzyMatch(query, item.detail));
  }, [allItems, query]);

  // Convert message results to palette items.
  const messageItems = useMemo((): PaletteItem[] => {
    return messageResults.map((m) => ({
      id: `msg-${m.id}`,
      label: truncate(m.content, 80),
      detail: `${m.author_name} in ${channelNameMap.get(m.channel_id) || m.channel_id}`,
      kind: "message" as const,
      channelId: m.channel_id,
      messageId: m.id,
    }));
  }, [messageResults, channelNameMap]);

  // Convert memory file results to palette items.
  const memoryItems = useMemo((): PaletteItem[] => {
    return memoryFiles.map((f) => ({
      id: `mem-${f.file_path}`,
      label: f.file_path.split("/").pop() || f.file_path,
      detail: f.dir_path,
      kind: "memory" as const,
      filePath: f.file_path,
    }));
  }, [memoryFiles]);

  // Combined list for keyboard navigation.
  const combined = useMemo(() => {
    return [...filteredChannels, ...memoryItems, ...messageItems];
  }, [filteredChannels, memoryItems, messageItems]);

  // Debounced message + memory search.
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    const q = query.trim();
    if (q.length < 2) {
      setMessageResults([]);
      setMemoryFiles([]);
      setSearching(false);
      return;
    }
    setSearching(true);
    debounceRef.current = setTimeout(async () => {
      const promises: Promise<void>[] = [];
      promises.push(
        searchMessages(q, 10)
          .then(setMessageResults)
          .catch(() => setMessageResults([])),
      );
      if (selectedChannelId) {
        promises.push(
          searchMemoryFiles(selectedChannelId, q)
            .then(setMemoryFiles)
            .catch(() => setMemoryFiles([])),
        );
      }
      await Promise.all(promises);
      setSearching(false);
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [query, selectedChannelId]);

  // Reset state when opened.
  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      setMessageResults([]);
      setMemoryFiles([]);
      setSearching(false);
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [open]);

  // Keep selected index in bounds.
  useEffect(() => {
    setSelectedIndex((i) => Math.min(i, Math.max(0, combined.length - 1)));
  }, [combined.length]);

  // Scroll selected item into view.
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const item = list.children[selectedIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex]);

  const handleItemSelect = useCallback(
    (item: PaletteItem) => {
      if (item.kind === "memory" && item.filePath) {
        if (onSelectMemoryFile) {
          onSelectMemoryFile(item.filePath);
        }
      } else if (item.kind === "message" && item.channelId && item.messageId) {
        if (onSelectMessage) {
          onSelectMessage(item.channelId, item.messageId);
        } else {
          onSelect(item.channelId);
        }
      } else {
        onSelect(item.id);
      }
      onClose();
    },
    [onSelect, onSelectMessage, onSelectMemoryFile, onClose],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setSelectedIndex((i) => Math.min(i + 1, combined.length - 1));
          break;
        case "ArrowUp":
          e.preventDefault();
          setSelectedIndex((i) => Math.max(i - 1, 0));
          break;
        case "Enter":
          e.preventDefault();
          if (combined[selectedIndex]) {
            handleItemSelect(combined[selectedIndex]);
          }
          break;
        case "Escape":
          e.preventDefault();
          onClose();
          break;
      }
    },
    [combined, selectedIndex, handleItemSelect, onClose],
  );

  if (!open) return null;

  const kindIcon = (kind: PaletteItem["kind"]) => {
    switch (kind) {
      case "channel":
        return "#";
      case "thread":
        return "┗";
      case "message":
        return "\uD83D\uDCAC";
      case "memory":
        return "\uD83E\uDDE0";
    }
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 1000,
        display: "flex",
        justifyContent: "center",
        paddingTop: 80,
        backgroundColor: colors.overlay,
      }}
      onClick={onClose}
    >
      <div
        style={{
          width: 520,
          maxHeight: 400,
          backgroundColor: colors.surface,
          borderRadius: 8,
          border: `1px solid ${colors.border}`,
          boxShadow: `0 16px 48px ${colors.shadow}`,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          alignSelf: "flex-start",
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ padding: "12px 12px 0" }}>
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIndex(0);
            }}
            onKeyDown={handleKeyDown}
            placeholder="Search channels, threads, messages, and memory..."
            style={{
              width: "100%",
              background: colors.bg,
              border: `1px solid ${colors.inputBorder}`,
              borderRadius: 6,
              color: colors.textLight,
              fontSize: 14,
              padding: "8px 12px",
              outline: "none",
              boxSizing: "border-box",
              fontFamily: fonts.sans,
            }}
          />
        </div>
        <div ref={listRef} style={{ flex: 1, overflow: "auto", padding: "8px 0" }}>
          {combined.length === 0 && !searching && <div style={{ padding: "12px 16px", color: colors.textDim, fontSize: 13 }}>No results</div>}
          {filteredChannels.map((item, i) => (
            <div
              key={item.id}
              onClick={() => handleItemSelect(item)}
              onMouseEnter={() => setSelectedIndex(i)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                padding: "6px 16px",
                cursor: "pointer",
                backgroundColor: i === selectedIndex ? colors.selectedBg : "transparent",
              }}
            >
              <span style={{ color: colors.textDim, fontSize: 12, flexShrink: 0 }}>{kindIcon(item.kind)}</span>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div
                  style={{
                    fontSize: 13,
                    color: colors.textLight,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {item.label}
                </div>
                <div
                  style={{
                    fontSize: 11,
                    color: colors.textDim,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {item.detail}
                </div>
              </div>
            </div>
          ))}
          {memoryItems.length > 0 && (
            <div
              style={{
                padding: "8px 16px 4px",
                fontSize: 11,
                fontWeight: 700,
                color: colors.textDim,
                textTransform: "uppercase",
                letterSpacing: 1,
                borderTop: filteredChannels.length > 0 ? `1px solid ${colors.border}` : undefined,
                marginTop: filteredChannels.length > 0 ? 4 : 0,
              }}
            >
              Memory
            </div>
          )}
          {memoryItems.map((item, i) => {
            const globalIndex = filteredChannels.length + i;
            return (
              <div
                key={item.id}
                onClick={() => handleItemSelect(item)}
                onMouseEnter={() => setSelectedIndex(globalIndex)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "6px 16px",
                  cursor: "pointer",
                  backgroundColor: globalIndex === selectedIndex ? colors.selectedBg : "transparent",
                }}
              >
                <span style={{ color: colors.textDim, fontSize: 12, flexShrink: 0 }}>{kindIcon(item.kind)}</span>
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      fontSize: 13,
                      color: colors.textLight,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      fontFamily: fonts.mono,
                    }}
                  >
                    {item.label}
                  </div>
                  <div
                    style={{
                      fontSize: 11,
                      color: colors.textDim,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {item.detail}
                  </div>
                </div>
              </div>
            );
          })}
          {messageItems.length > 0 && (
            <div
              style={{
                padding: "8px 16px 4px",
                fontSize: 11,
                fontWeight: 700,
                color: colors.textDim,
                textTransform: "uppercase",
                letterSpacing: 1,
                borderTop: filteredChannels.length > 0 || memoryItems.length > 0 ? `1px solid ${colors.border}` : undefined,
                marginTop: filteredChannels.length > 0 || memoryItems.length > 0 ? 4 : 0,
              }}
            >
              Messages
            </div>
          )}
          {messageItems.map((item, i) => {
            const globalIndex = filteredChannels.length + memoryItems.length + i;
            return (
              <div
                key={item.id}
                onClick={() => handleItemSelect(item)}
                onMouseEnter={() => setSelectedIndex(globalIndex)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  padding: "6px 16px",
                  cursor: "pointer",
                  backgroundColor: globalIndex === selectedIndex ? colors.selectedBg : "transparent",
                }}
              >
                <span style={{ color: colors.textDim, fontSize: 12, flexShrink: 0 }}>{kindIcon(item.kind)}</span>
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      fontSize: 13,
                      color: colors.textLight,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      fontFamily: fonts.mono,
                    }}
                  >
                    {item.label}
                  </div>
                  <div
                    style={{
                      fontSize: 11,
                      color: colors.textDim,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {item.detail}
                  </div>
                </div>
              </div>
            );
          })}
          {searching && <div style={{ padding: "8px 16px", color: colors.textDim, fontSize: 12 }}>Searching messages...</div>}
        </div>
      </div>
    </div>
  );
}
