import { useRef, useState } from "react";
import { deleteQueuedMessage, reorderQueuedMessages, steerQueuedMessage } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Message } from "../../types";
import { logErr } from "../../utils/log";
import { DelayCountdown } from "./DelayCountdown";

interface QueuedMessagesPopupProps {
  messages: Message[];
  channelId: string;
  // Steering only means something while a turn is in flight — with the channel
  // idle the queue is already draining, so the row would just be reordering
  // itself. The button is hidden rather than disabled to keep the row quiet.
  isRunning?: boolean;
}

export function QueuedMessagesPopup({ messages, channelId, isRunning }: QueuedMessagesPopupProps) {
  const { colors } = useTheme();
  const [expanded, setExpanded] = useState(false);
  const [expandedRowIds, setExpandedRowIds] = useState<Set<string>>(new Set());
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set());
  const [steeringIds, setSteeringIds] = useState<Set<string>>(new Set());
  const [copiedIds, setCopiedIds] = useState<Set<string>>(new Set());
  const [order, setOrder] = useState<string[] | null>(null);
  // The row the cursor is over while dragging, plus which edge the dragged item
  // would land on — drives the drop-line indicator and the insert position.
  const [dropTarget, setDropTarget] = useState<{ id: string; pos: "before" | "after" } | null>(null);
  const draggedIdRef = useRef<string | null>(null);

  if (messages.length === 0) return null;

  const toggleRow = (msgId: string) => {
    setExpandedRowIds((prev) => {
      const next = new Set(prev);
      if (next.has(msgId)) next.delete(msgId);
      else next.add(msgId);
      return next;
    });
  };

  const handleCopy = async (msgId: string, content: string) => {
    try {
      await navigator.clipboard.writeText(content);
    } catch {
      return;
    }
    setCopiedIds((prev) => new Set(prev).add(msgId));
    setTimeout(() => {
      setCopiedIds((prev) => {
        const next = new Set(prev);
        next.delete(msgId);
        return next;
      });
    }, 1200);
  };

  // Steering stops the run and hands the agent this row next. The local order
  // is updated too: the backend has already re-prioritised it, and without this
  // the row would keep its old position until the queue refetches.
  const handleSteer = async (msgId: string) => {
    setSteeringIds((prev) => new Set(prev).add(msgId));
    try {
      await steerQueuedMessage(channelId, msgId);
      setOrder([msgId, ...displayed.map((m) => m.msg_id).filter((id) => id !== msgId)]);
    } catch {
      // Leave the row where it is — the queue is unchanged if the call failed.
    }
    setSteeringIds((prev) => {
      const next = new Set(prev);
      next.delete(msgId);
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

  // Apply the user's drag order locally: known ids first (in chosen order),
  // then any newly-arrived messages appended; removed ones drop out.
  const displayed = (() => {
    if (!order) return messages;
    const byId = new Map(messages.map((m) => [m.msg_id, m]));
    const result: Message[] = [];
    for (const id of order) {
      const m = byId.get(id);
      if (m) {
        result.push(m);
        byId.delete(id);
      }
    }
    for (const m of messages) if (byId.has(m.msg_id)) result.push(m);
    return result;
  })();

  // dropPosition returns "before" or "after" depending on whether the cursor is
  // in the top or bottom half of the row being hovered.
  const dropPosition = (e: React.DragEvent): "before" | "after" => {
    const rect = e.currentTarget.getBoundingClientRect();
    return e.clientY < rect.top + rect.height / 2 ? "before" : "after";
  };

  const handleDrop = (targetId: string, pos: "before" | "after") => {
    const dragged = draggedIdRef.current;
    draggedIdRef.current = null;
    setDropTarget(null);
    if (!dragged) return;
    const original = displayed.map((m) => m.msg_id);
    const ids = original.slice();
    const from = ids.indexOf(dragged);
    if (from < 0) return;
    ids.splice(from, 1);
    let to = ids.indexOf(targetId);
    if (to < 0) return; // target was the dragged row itself
    if (pos === "after") to += 1;
    ids.splice(to, 0, dragged);
    if (ids.length === original.length && ids.every((id, i) => id === original[i])) return; // no-op
    setOrder(ids);
    reorderQueuedMessages(channelId, ids).catch(logErr("reordering queued messages"));
  };

  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        padding: "0 24px",
        // Cancel the input bar's 12px top padding so the card's bottom edge
        // meets the composer's top edge with no seam — the queue is meant to
        // read as one surface continuing into the input, not a separate card.
        marginBottom: -12,
      }}
    >
      <div
        style={{
          width: "100%",
          // Inset 12px per side from the composer's 768 so the queue sits *on
          // top of* the input rather than flush with it — the stacked look the
          // card is copying. Rounded on top only; the bottom runs into the
          // composer, and its side borders line up with nothing below on
          // purpose.
          maxWidth: 744,
          borderRadius: "12px 12px 0 0",
          overflow: "hidden",
          // Dashed, and a shade brighter than the composer's solid border, so
          // the queue tray stays legible as its own thing even though it runs
          // into the input below it.
          border: `1px dashed ${colors.inputBorder}`,
          borderBottom: "none",
          backgroundColor: colors.surface,
          fontFamily: fonts.mono,
          fontSize: 12,
        }}
      >
        <button
          onClick={() => setExpanded((v) => !v)}
          style={{
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "8px 18px",
            background: "none",
            border: "none",
            color: colors.textMuted,
            cursor: "pointer",
            fontFamily: fonts.mono,
            fontSize: 12,
          }}
        >
          <span>
            <span style={{ fontWeight: 700, color: colors.textLight }}>{messages.length}</span> queued
          </span>
          <span style={{ fontSize: 10, opacity: 0.7 }}>{expanded ? "\u25B4" : "\u25BE"}</span>
        </button>
        {expanded && (
          <div style={{ borderTop: `1px solid ${colors.border}` }}>
            {displayed.map((msg) => {
              const isRowExpanded = expandedRowIds.has(msg.msg_id);
              const isDeleting = deletingIds.has(msg.msg_id);
              return (
                <div
                  key={msg.msg_id}
                  onDragOver={(e) => {
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                    const pos = dropPosition(e);
                    setDropTarget((prev) => (prev && prev.id === msg.msg_id && prev.pos === pos ? prev : { id: msg.msg_id, pos }));
                  }}
                  onDrop={(e) => handleDrop(msg.msg_id, dropPosition(e))}
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: 8,
                    padding: "6px 18px",
                    opacity: isDeleting ? 0.5 : 1,
                    boxShadow: dropTarget && dropTarget.id === msg.msg_id ? (dropTarget.pos === "before" ? `inset 0 2px 0 0 ${colors.active}` : `inset 0 -2px 0 0 ${colors.active}`) : undefined,
                  }}
                >
                  <span
                    draggable
                    onDragStart={(e) => {
                      draggedIdRef.current = msg.msg_id;
                      e.dataTransfer.effectAllowed = "move";
                    }}
                    onDragEnd={() => {
                      draggedIdRef.current = null;
                      setDropTarget(null);
                    }}
                    title="Drag to reorder"
                    style={{ flexShrink: 0, cursor: "grab", color: colors.textDim, userSelect: "none", display: "flex", alignItems: "center", paddingTop: 2 }}
                  >
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                      <circle cx="9" cy="6" r="1.5" />
                      <circle cx="15" cy="6" r="1.5" />
                      <circle cx="9" cy="12" r="1.5" />
                      <circle cx="15" cy="12" r="1.5" />
                      <circle cx="9" cy="18" r="1.5" />
                      <circle cx="15" cy="18" r="1.5" />
                    </svg>
                  </span>
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
                  {msg.not_before ? <DelayCountdown notBefore={msg.not_before} /> : null}
                  {isRunning ? (
                    <button
                      onClick={() => handleSteer(msg.msg_id)}
                      disabled={steeringIds.has(msg.msg_id) || isDeleting}
                      title="Stop the agent and send this next"
                      style={{
                        flexShrink: 0,
                        display: "flex",
                        alignItems: "center",
                        gap: 3,
                        height: 20,
                        padding: "0 4px",
                        background: "none",
                        border: "none",
                        color: colors.textDim,
                        cursor: steeringIds.has(msg.msg_id) ? "default" : "pointer",
                        fontFamily: fonts.mono,
                        fontSize: 11,
                        borderRadius: 4,
                      }}
                      onMouseEnter={(e) => {
                        if (!steeringIds.has(msg.msg_id)) e.currentTarget.style.color = colors.textLight;
                      }}
                      onMouseLeave={(e) => {
                        e.currentTarget.style.color = colors.textDim;
                      }}
                    >
                      <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="15 10 20 15 15 20" />
                        <path d="M4 4v7a4 4 0 0 0 4 4h12" />
                      </svg>
                      Steer
                    </button>
                  ) : null}
                  <button
                    onClick={() => handleCopy(msg.msg_id, msg.content)}
                    title={copiedIds.has(msg.msg_id) ? "Copied" : "Copy to clipboard"}
                    style={{
                      flexShrink: 0,
                      width: 20,
                      height: 20,
                      padding: 0,
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                      background: "none",
                      border: "none",
                      color: copiedIds.has(msg.msg_id) ? colors.active : colors.textDim,
                      cursor: "pointer",
                      borderRadius: 4,
                    }}
                    onMouseEnter={(e) => {
                      if (!copiedIds.has(msg.msg_id)) e.currentTarget.style.color = colors.textLight;
                    }}
                    onMouseLeave={(e) => {
                      if (!copiedIds.has(msg.msg_id)) e.currentTarget.style.color = colors.textDim;
                    }}
                  >
                    {copiedIds.has(msg.msg_id) ? (
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                    ) : (
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                      </svg>
                    )}
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
                    onMouseEnter={(e) => {
                      if (!isDeleting) e.currentTarget.style.color = colors.dangerText;
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.color = colors.textDim;
                    }}
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
