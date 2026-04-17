import { useState } from "react";
import type { Message } from "../../types";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { deleteQueuedMessage } from "../../api/loopApi";

interface QueuedMessagesPopupProps {
  messages: Message[];
  channelId: string;
}

export function QueuedMessagesPopup({ messages, channelId }: QueuedMessagesPopupProps) {
  const { colors } = useTheme();
  const [expanded, setExpanded] = useState(false);
  const [expandedRowIds, setExpandedRowIds] = useState<Set<string>>(new Set());
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set());

  if (messages.length === 0) return null;

  const toggleRow = (msgId: string) => {
    setExpandedRowIds((prev) => {
      const next = new Set(prev);
      if (next.has(msgId)) next.delete(msgId);
      else next.add(msgId);
      return next;
    });
  };

  const handleDelete = async (msgId: string) => {
    setDeletingIds((prev) => new Set(prev).add(msgId));
    try {
      await deleteQueuedMessage(channelId, msgId);
      // The WS `message.deleted` event will remove the message from state.
    } catch {
      setDeletingIds((prev) => {
        const next = new Set(prev);
        next.delete(msgId);
        return next;
      });
    }
  };

  return (
    <div style={{ display: "flex", justifyContent: "center", padding: "4px 24px 0" }}>
      <div style={{
        width: "100%",
        maxWidth: 768,
        borderRadius: 8,
        border: `1px solid ${colors.border}`,
        backgroundColor: colors.surface,
        fontFamily: fonts.mono,
        fontSize: 12,
      }}>
        <button
          onClick={() => setExpanded((v) => !v)}
          style={{
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "8px 14px",
            background: "none",
            border: "none",
            color: colors.textMuted,
            cursor: "pointer",
            fontFamily: fonts.mono,
            fontSize: 12,
          }}
        >
          <span>
            <span style={{ fontWeight: 700, color: colors.textLight }}>{messages.length}</span>
            {" "}queued
          </span>
          <span style={{ fontSize: 10, opacity: 0.7 }}>{expanded ? "\u25B4" : "\u25BE"}</span>
        </button>
        {expanded && (
          <div style={{ borderTop: `1px solid ${colors.border}` }}>
            {messages.map((msg) => {
              const isRowExpanded = expandedRowIds.has(msg.msg_id);
              const isDeleting = deletingIds.has(msg.msg_id);
              return (
                <div
                  key={msg.msg_id}
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: 8,
                    padding: "6px 14px",
                    borderBottom: `1px solid ${colors.border}`,
                    opacity: isDeleting ? 0.5 : 1,
                  }}
                >
                  <button
                    onClick={() => toggleRow(msg.msg_id)}
                    style={{
                      flex: 1,
                      textAlign: "left",
                      background: "none",
                      border: "none",
                      padding: 0,
                      color: colors.text,
                      cursor: "pointer",
                      fontFamily: fonts.mono,
                      fontSize: 12,
                      lineHeight: 1.5,
                      whiteSpace: isRowExpanded ? "pre-wrap" : "nowrap",
                      overflow: isRowExpanded ? "visible" : "hidden",
                      textOverflow: isRowExpanded ? "clip" : "ellipsis",
                      minWidth: 0,
                      wordBreak: isRowExpanded ? "break-word" : "normal",
                    }}
                    title={isRowExpanded ? "Click to collapse" : "Click to expand"}
                  >
                    {msg.content}
                  </button>
                  <button
                    onClick={() => handleDelete(msg.msg_id)}
                    disabled={isDeleting}
                    title="Remove from queue"
                    style={{
                      flexShrink: 0,
                      width: 20,
                      height: 20,
                      padding: 0,
                      background: "none",
                      border: "none",
                      color: colors.textDim,
                      cursor: isDeleting ? "default" : "pointer",
                      fontSize: 14,
                      lineHeight: 1,
                      borderRadius: 4,
                    }}
                    onMouseEnter={(e) => { if (!isDeleting) e.currentTarget.style.color = colors.dangerText; }}
                    onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
                  >
                    {"\u00D7"}
                  </button>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
