import { createContext, forwardRef, useCallback, useContext, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { AgentActivityData, AskUserQuestion, ExitPlanModeData, Message, TimelineItem, TodoItem } from "../../types";
import type { ChatState } from "../../hooks/useChatState";
import { resolveAsk, resolvePlan } from "../../api/channels";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { ContextMenu } from "../shared/ContextMenu";
import type { MenuItem } from "../shared/ContextMenu";
import { QueuedMessagesPopup } from "./QueuedMessagesPopup";
import { ApprovalCard } from "./ApprovalCard";
import { FileLink } from "./FileLink";
import { findCandidatePaths } from "../../utils/fileLinks";

function buildMessageStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    messages: {
      flex: 1,
      overflowY: "auto",
      padding: "16px 24px",
    },
    messageColumn: {
      maxWidth: 768,
      margin: "0 auto",
    },
    loadMore: {
      display: "block",
      margin: "0 auto 16px",
      padding: "4px 12px",
      background: "none",
      border: `1px solid ${colors.border}`,
      borderRadius: 4,
      color: colors.textMuted,
      cursor: "pointer",
      fontFamily: fonts.sans,
      fontSize: 12,
    },
    bubble: {},
    header: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      marginBottom: 4,
    },
    author: {
      fontWeight: 600,
      fontSize: 13,
    },
    time: {
      fontSize: 11,
      color: colors.textDim,
    },
    content: {
      fontSize: 14,
      lineHeight: 1.6,
      color: colors.text,
      wordBreak: "break-word" as const,
    },
    paragraph: {
      margin: "2px 0",
    },
    codeBlock: {
      backgroundColor: colors.surface,
      borderRadius: 8,
      padding: "10px 14px",
      margin: "8px 0",
      overflow: "auto",
      fontFamily: fonts.mono,
      fontSize: 13,
      lineHeight: 1.4,
      color: colors.textLight,
    },
    codeLang: {
      fontSize: 11,
      color: colors.textDim,
      marginBottom: 4,
    },
    inlineCode: {
      backgroundColor: colors.surface,
      borderRadius: 3,
      padding: "1px 5px",
      fontFamily: fonts.mono,
      fontSize: 13,
    },
    blockquote: {
      borderLeft: `3px solid ${colors.border}`,
      paddingLeft: 12,
      margin: "6px 0",
      color: colors.textMuted,
      fontSize: 13,
      lineHeight: 1.5,
    },
    table: {
      borderCollapse: "collapse" as const,
      margin: "8px 0",
      fontSize: 13,
      lineHeight: 1.4,
      display: "block",
      maxWidth: "100%",
      overflowX: "auto" as const,
    },
    tableHeaderCell: {
      border: `1px solid ${colors.border}`,
      padding: "6px 10px",
      backgroundColor: colors.surface,
      fontWeight: 600,
      textAlign: "left" as const,
      whiteSpace: "nowrap" as const,
    },
    tableCell: {
      border: `1px solid ${colors.border}`,
      padding: "6px 10px",
      verticalAlign: "top" as const,
    },
  };
}

function buildActivityStyle(colors: ColorPalette): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: 8,
    marginBottom: 8,
    padding: "4px 0",
    fontSize: 12,
    color: colors.textDim,
    fontFamily: fonts.mono,
  };
}

// ChannelContext lets nested renderers (MarkdownContent, ToolActivityIndicator)
// resolve the current channel without prop drilling through every helper.
const ChannelContext = createContext<string>("");

export interface ChatMessagesProps {
  channelId: string;
  chatState: ChatState;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
  onQuote?: (msg: Message) => void;
}

export interface ChatMessagesHandle {
  scrollToBottom: () => void;
}

export const ChatMessages = forwardRef<ChatMessagesHandle, ChatMessagesProps>(function ChatMessages({ channelId, chatState, scrollToMessageId, onScrollComplete, onQuote }, ref) {
  const { colors } = useTheme();
  const styles = buildMessageStyles(colors);
  const { items, liveTail, messages, loading, loadMore, hasMore, streamingContent, isRunning, agentActivity, askUserQuestions, exitPlanRequest, todos, completionInfo, triggerContent, gateApproval, gateApprovalSource, processingMsgId } = chatState;
  // The approval card belongs to chat only when the gate is attributed to the
  // chat agent. Terminal-sourced gates ("terminal:<leafId>") render in the
  // matching terminal pane instead.
  const showGateApproval = gateApproval && gateApprovalSource === "chat";
  // Pair tool_use with its tool_result by tool_use_id so the renderer can
  // collapse them into a single pill with output. Skip pairing when the
  // tool_use_id is empty (live events without a stable id).
  const allItems: TimelineItem[] = [...items, ...liveTail];
  const resultsByToolUseID = new Map<string, { text: string; is_error: boolean; truncated: boolean }>();
  for (const it of allItems) {
    if (it.kind === "tool_result" && it.tool_use_id) {
      resultsByToolUseID.set(it.tool_use_id, { text: it.text, is_error: it.is_error ?? false, truncated: it.truncated ?? false });
    }
  }
  const skippedToolResultIDs = new Set<string>();
  for (const it of allItems) {
    if (it.kind === "tool_use" && it.tool_use_id && resultsByToolUseID.has(it.tool_use_id)) {
      skippedToolResultIDs.add(it.tool_use_id);
    }
  }
  // While compacting is actively in progress the bottom "Compacting context..."
  // indicator is showing — suppress the trailing in-timeline "Compacted context"
  // marker so we don't render the past-tense stamp before the action finishes.
  // Once agentActivity transitions away (to "model" etc.) the marker reappears.
  let suppressedCompactingId: number | null = null;
  if (isRunning && agentActivity?.activity === "compacting") {
    for (let i = allItems.length - 1; i >= 0; i--) {
      if (allItems[i]!.kind === "compacting") {
        suppressedCompactingId = allItems[i]!.id;
        break;
      }
    }
  }
  const visibleAllItems = suppressedCompactingId !== null
    ? allItems.filter((it) => !(it.kind === "compacting" && it.id === suppressedCompactingId))
    : allItems;
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);
  const [highlightedMsgId, setHighlightedMsgId] = useState<number | null>(null);

  const scrollToBottom = useCallback(() => {
    autoScrollRef.current = true;
    requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ behavior: "smooth" }));
  }, []);

  useImperativeHandle(ref, () => ({ scrollToBottom }), [scrollToBottom]);

  // Auto-scroll to bottom on new messages, timeline growth, or streaming updates.
  useEffect(() => {
    if (autoScrollRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages, items, liveTail, streamingContent, agentActivity, askUserQuestions, exitPlanRequest, todos, gateApproval]);

  // Scroll to a specific message (from search) and highlight it.
  useEffect(() => {
    if (!scrollToMessageId || !containerRef.current) return;
    const el = containerRef.current.querySelector(`[data-msg-id="${scrollToMessageId}"]`);
    if (el) {
      autoScrollRef.current = false;
      el.scrollIntoView({ behavior: "smooth", block: "center" });
      setHighlightedMsgId(scrollToMessageId);
      onScrollComplete?.();
      const timer = setTimeout(() => setHighlightedMsgId(null), 2000);
      return () => clearTimeout(timer);
    }
  }, [scrollToMessageId, messages]);

  // Track whether user has scrolled up.
  const handleScroll = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    autoScrollRef.current = atBottom;

    // Load more when scrolled to top.
    if (el.scrollTop === 0 && hasMore && !loading) {
      loadMore();
    }
  }, [hasMore, loading, loadMore]);

  // The backend tells us which msg_id the agent is currently processing via
  // agent.status. We must NOT infer from array position because priority-bumped
  // messages (e.g. deny-with-prompt) can be processed out of chronological
  // order. Fallback: during the brief startup window before the first
  // agent.status event arrives, use the first-unprocessed heuristic.
  const unprocessedUserMsgs = messages.filter((m) => !m.is_bot && !m.is_processed);
  const effectiveProcessingMsgId =
    processingMsgId ?? (isRunning ? unprocessedUserMsgs[0]?.msg_id ?? null : null);
  // Messages waiting behind the currently-processing one; deletable from the popup.
  const queuedMessages = unprocessedUserMsgs.filter((m) => m.msg_id !== effectiveProcessingMsgId);
  const hasQueuedMessages = queuedMessages.length > 0;

  // Queue position labels ("1/3" etc.) keyed by msg_id. The backend processes
  // rows in (priority DESC, id ASC) order, so we sort by the same rule to
  // match what the agent will actually run next. Higher priority lands on top
  // (an interrupt becomes "1/N" and bumps existing rows down).
  const queueOrdered = [...queuedMessages].sort((a, b) => {
    const pa = a.priority ?? 0;
    const pb = b.priority ?? 0;
    if (pa !== pb) return pb - pa;
    return a.id - b.id;
  });
  const queuePositionByMsgId = new Map<string, string>();
  queueOrdered.forEach((m, idx) => {
    queuePositionByMsgId.set(m.msg_id, `${idx + 1}/${queueOrdered.length}`);
  });

  // Track whether we ever had queued messages in this batch, so the trigger
  // quote persists even when processing the last message of a multi-message batch.
  const hadQueuedRef = useRef(false);
  if (hasQueuedMessages) hadQueuedRef.current = true;
  if (unprocessedUserMsgs.length === 0) hadQueuedRef.current = false;
  const showTriggerQuote = isRunning && !!triggerContent && (hasQueuedMessages || hadQueuedRef.current);

  return (
    <ChannelContext.Provider value={channelId}>
      <div ref={containerRef} style={styles.messages} onScroll={handleScroll}>
        <div style={styles.messageColumn}>
          {hasMore && (
            <button onClick={loadMore} style={styles.loadMore}>
              {loading ? "Loading..." : "Load older messages"}
            </button>
          )}
          {(() => {
            const groups = groupTimelineItems(visibleAllItems);
            const lastIdx = groups.length - 1;
            return groups.map((g, idx) => {
              if (g.kind === "message") {
                const msg = g.data.data;
                return (
                  <MessageBubble
                    key={`m-${msg.msg_id}`}
                    message={msg}
                    showProcessing={isRunning && !msg.is_bot && msg.msg_id === effectiveProcessingMsgId}
                    showQueued={!msg.is_bot && !msg.is_processed && msg.msg_id !== effectiveProcessingMsgId}
                    queuePosition={queuePositionByMsgId.get(msg.msg_id)}
                    highlighted={msg.id === highlightedMsgId}
                    onQuote={onQuote}
                  />
                );
              }
              const visible = g.items.filter((it) => !(it.kind === "tool_result" && it.tool_use_id && skippedToolResultIDs.has(it.tool_use_id)));
              if (visible.length === 0) return null;
              if (visible.length === 1) {
                return (
                  <div key={`g-${g.items[0]!.id}`}>
                    {renderTimelineItem(visible[0]!, resultsByToolUseID, skippedToolResultIDs)}
                  </div>
                );
              }
              return (
                <ToolRunBlock
                  key={`g-${g.items[0]!.id}`}
                  items={visible}
                  resultsByToolUseID={resultsByToolUseID}
                  skippedToolResultIDs={skippedToolResultIDs}
                  isActive={idx === lastIdx}
                />
              );
            });
          })()}
          {showTriggerQuote && (
            <TriggerQuote content={triggerContent} time={effectiveProcessingMsgId ? messages.find((m) => m.msg_id === effectiveProcessingMsgId)?.created_at : undefined} />
          )}
          {isRunning && agentActivity && (
            <AgentActivityIndicator activity={agentActivity} />
          )}
          {showGateApproval && (
            <ApprovalCard data={gateApproval} channelId={channelId} onResolved={() => { chatState.clearGateApproval(); scrollToBottom(); }} />
          )}
          {askUserQuestions && !isRunning && channelId && (
            <AskUserQuestionCard questions={askUserQuestions.questions} channelId={channelId} mode={chatState.mode} onSent={() => { chatState.clearAskUser(); scrollToBottom(); }} />
          )}
          {exitPlanRequest && !askUserQuestions && !isRunning && channelId && (
            <ExitPlanCard plan={exitPlanRequest} channelId={channelId} setMode={chatState.setMode} onSent={() => { chatState.clearExitPlan(); scrollToBottom(); }} />
          )}
          {streamingContent && (
            <StreamingBubble content={streamingContent} />
          )}
          {completionInfo && !isRunning && (
            <CompletionSummary info={completionInfo} />
          )}
          <div ref={bottomRef} />
        </div>
      </div>
      {todos && (
        <TodoChecklist todos={todos.todos} />
      )}
      {queuedMessages.length > 0 && (
        <QueuedMessagesPopup messages={queuedMessages} channelId={channelId} />
      )}
    </ChannelContext.Provider>
  );
});

type TimelineGroup =
  | { kind: "message"; data: Extract<TimelineItem, { kind: "message" }> }
  | { kind: "agent"; items: TimelineItem[] };

function groupTimelineItems(items: TimelineItem[]): TimelineGroup[] {
  const out: TimelineGroup[] = [];
  let bucket: TimelineItem[] = [];
  const flush = () => {
    if (bucket.length) {
      out.push({ kind: "agent", items: bucket });
      bucket = [];
    }
  };
  for (const it of items) {
    if (it.kind === "message") {
      flush();
      out.push({ kind: "message", data: it });
    } else {
      bucket.push(it);
    }
  }
  flush();
  return out;
}

function renderTimelineItem(
  it: TimelineItem,
  resultsByToolUseID: Map<string, { text: string; is_error: boolean; truncated: boolean }>,
  skippedToolResultIDs: Set<string>,
): React.ReactNode {
  if (it.kind === "thinking") {
    return <ThinkingBubble key={`t-${it.id}`} text={it.text} truncated={it.truncated ?? false} />;
  }
  if (it.kind === "tool_use") {
    if (it.tool_use_id && skippedToolResultIDs.has(it.tool_use_id)) {
      const result = resultsByToolUseID.get(it.tool_use_id)!;
      return <ToolActivityIndicator key={`tu-${it.id}`} toolName={it.tool_name} input={it.tool_input} result={result} />;
    }
    return <ToolActivityIndicator key={`tu-${it.id}`} toolName={it.tool_name} input={it.tool_input} />;
  }
  if (it.kind === "tool_result") {
    if (it.tool_use_id && skippedToolResultIDs.has(it.tool_use_id)) return null;
    return <ToolActivityIndicator key={`tr-${it.id}`} toolName="result" input="" result={{ text: it.text, is_error: it.is_error ?? false, truncated: it.truncated ?? false }} />;
  }
  if (it.kind === "compacting") {
    return <CompactingMarker key={`c-${it.id}`} />;
  }
  return null;
}

function CompactingMarker() {
  const { colors } = useTheme();
  const activityStyle = buildActivityStyle(colors);
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }} dangerouslySetInnerHTML={{ __html: "&#128220;" }} />
      <span style={{ color: colors.textMuted }}>Compacted context</span>
    </div>
  );
}

function ToolRunBlock({ items, resultsByToolUseID, skippedToolResultIDs, isActive }: {
  items: TimelineItem[];
  resultsByToolUseID: Map<string, { text: string; is_error: boolean; truncated: boolean }>;
  skippedToolResultIDs: Set<string>;
  isActive: boolean;
}) {
  const { colors } = useTheme();
  const [expanded, setExpanded] = useState(isActive);
  const wasActiveRef = useRef(isActive);
  // When the run is no longer the trailing one (a message arrived after it),
  // auto-collapse. When it becomes trailing again (rare), auto-expand. The
  // user can still toggle manually after the transition.
  useEffect(() => {
    if (wasActiveRef.current && !isActive) setExpanded(false);
    if (!wasActiveRef.current && isActive) setExpanded(true);
    wasActiveRef.current = isActive;
  }, [isActive]);

  const toolNames: string[] = [];
  let thinkingCount = 0;
  let errorCount = 0;
  let compactingCount = 0;
  for (const it of items) {
    if (it.kind === "thinking") thinkingCount++;
    if (it.kind === "tool_use") {
      if (!toolNames.includes(it.tool_name)) toolNames.push(it.tool_name);
    }
    if (it.kind === "tool_result" && it.is_error) errorCount++;
    if (it.kind === "compacting") compactingCount++;
  }
  const summaryParts: string[] = [];
  if (toolNames.length > 0) {
    const shown = toolNames.slice(0, 4).join(", ");
    summaryParts.push(toolNames.length > 4 ? `${shown}, +${toolNames.length - 4}` : shown);
  }
  if (thinkingCount > 0) summaryParts.push(`${thinkingCount} thought${thinkingCount === 1 ? "" : "s"}`);
  if (compactingCount > 0) summaryParts.push(compactingCount === 1 ? "compacted" : `${compactingCount} compactions`);
  const summary = summaryParts.join(" · ");

  return (
    <div style={{ marginBottom: 8 }}>
      <div
        onClick={() => setExpanded((v) => !v)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "4px 8px",
          marginBottom: 4,
          borderRadius: 6,
          border: `1px solid ${colors.border}`,
          backgroundColor: colors.surface,
          cursor: "pointer",
          fontFamily: fonts.mono,
          fontSize: 12,
          color: colors.textMuted,
          userSelect: "none",
        }}
      >
        <span style={{ display: "inline-block", width: 10, transform: expanded ? "rotate(90deg)" : "none", transition: "transform 0.1s" }}>
          &#9654;
        </span>
        <span style={{ fontWeight: 600 }}>{items.length} step{items.length === 1 ? "" : "s"}</span>
        {summary && <span style={{ color: colors.textDim }}>· {summary}</span>}
        {errorCount > 0 && (
          <span style={{ marginLeft: "auto", color: colors.warning, fontSize: 11 }}>
            {errorCount} error{errorCount === 1 ? "" : "s"}
          </span>
        )}
      </div>
      {expanded && (
        <div style={{ paddingLeft: 12, borderLeft: `1px solid ${colors.border}`, marginLeft: 4 }}>
          {items.map((it) => renderTimelineItem(it, resultsByToolUseID, skippedToolResultIDs))}
        </div>
      )}
    </div>
  );
}

function MessageBubble({ message, showProcessing, showQueued, queuePosition, highlighted, onQuote }: { message: Message; showProcessing?: boolean; showQueued?: boolean; queuePosition?: string; highlighted?: boolean; onQuote?: (msg: Message) => void }) {
  const { colors } = useTheme();
  const styles = buildMessageStyles(colors);
  const isUser = !message.is_bot;
  const time = new Date(message.created_at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    if (!onQuote) return;
    e.preventDefault();
    setCtxMenu({
      x: e.clientX,
      y: e.clientY,
      items: [{ label: "Quote reply", onClick: () => onQuote(message) }],
    });
  }, [onQuote, message]);

  return (
    <div
      data-msg-id={message.id}
      onContextMenu={handleContextMenu}
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: isUser ? "flex-end" : "flex-start",
        marginBottom: 16,
        borderRadius: 8,
        transition: "background-color 0.5s ease",
        backgroundColor: highlighted ? "rgba(99, 102, 241, 0.15)" : "transparent",
        padding: highlighted ? "4px 8px" : 0,
      }}
    >
      {ctxMenu && <ContextMenu x={ctxMenu.x} y={ctxMenu.y} items={ctxMenu.items} onClose={() => setCtxMenu(null)} />}
      <div
        style={{
          ...styles.bubble,
          backgroundColor: isUser ? colors.userBubble : "transparent",
          borderRadius: isUser ? "18px 18px 4px 18px" : "18px 18px 18px 4px",
          maxWidth: "85%",
          padding: isUser ? "10px 16px" : "4px 0",
        }}
      >
        {!isUser && (
          <div style={styles.header}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ flexShrink: 0 }}>
              <rect x="4" y="6" width="16" height="14" rx="3" stroke={colors.textLight} strokeWidth="1.5"/>
              <circle cx="9" cy="12" r="2" fill={colors.textLight}/>
              <circle cx="15" cy="12" r="2" fill={colors.textLight}/>
              <line x1="12" y1="2" x2="12" y2="6" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
              <circle cx="12" cy="2" r="1.5" fill={colors.textLight}/>
              <line x1="1" y1="11" x2="4" y2="11" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
              <line x1="20" y1="11" x2="23" y2="11" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
              <line x1="9" y1="17" x2="15" y2="17" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
            </svg>
            <span style={styles.time}>{time}</span>
          </div>
        )}
        <div style={styles.content}>
          <MarkdownContent content={message.content} />
        </div>
        {isUser && (
          <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 6, marginTop: 4 }}>
            {showQueued && <span style={{ fontSize: 10, color: colors.textDim, background: colors.surface, border: "1px solid rgba(255,255,255,0.2)", borderRadius: 4, padding: "2px 8px", fontWeight: 500, letterSpacing: 0.3 }}>{queuePosition ? `queued ${queuePosition}` : "queued"}</span>}
            {showProcessing && <span style={{ fontSize: 10, color: colors.textMuted, background: colors.surface, border: "1px solid rgba(255,255,255,0.2)", borderRadius: 4, padding: "2px 8px", fontWeight: 500, letterSpacing: 0.3 }}>processing</span>}
            <span style={styles.time}>{time}</span>
          </div>
        )}
      </div>
    </div>
  );
}

function StreamingBubble({ content }: { content: string }) {
  const { colors } = useTheme();
  const styles = buildMessageStyles(colors);
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "flex-start",
        marginBottom: 16,
      }}
    >
      <div
        style={{
          borderRadius: "18px 18px 18px 4px",
          maxWidth: "85%",
          padding: "4px 0",
        }}
      >
        <div style={styles.header}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ flexShrink: 0 }}>
            <rect x="4" y="6" width="16" height="14" rx="3" stroke={colors.textLight} strokeWidth="1.5"/>
            <circle cx="9" cy="12" r="2" fill={colors.textLight}/>
            <circle cx="15" cy="12" r="2" fill={colors.textLight}/>
            <line x1="12" y1="2" x2="12" y2="6" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
            <circle cx="12" cy="2" r="1.5" fill={colors.textLight}/>
            <line x1="1" y1="11" x2="4" y2="11" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
            <line x1="20" y1="11" x2="23" y2="11" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
            <line x1="9" y1="17" x2="15" y2="17" stroke={colors.textLight} strokeWidth="1.5" strokeLinecap="round"/>
          </svg>
          <span style={{ ...styles.time, fontStyle: "italic" }}>streaming...</span>
        </div>
        <div style={styles.content}>
          <MarkdownContent content={content} />
        </div>
      </div>
    </div>
  );
}

function TriggerQuote({ content, time }: { content: string; time?: string }) {
  const { colors } = useTheme();
  const text = content.length > 120 ? content.slice(0, 120) + "\u2026" : content;
  const timeStr = time ? new Date(time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : null;
  return (
    <div style={{
      display: "flex",
      alignItems: "center",
      gap: 8,
      padding: "6px 12px",
      margin: "4px 0 8px",
      borderRadius: 6,
      backgroundColor: colors.surface,
      border: `1px solid ${colors.border}`,
    }}>
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" style={{ flexShrink: 0, opacity: 0.5 }}>
        <path d="M14 10l-3 3-3-3" stroke={colors.textDim} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        <path d="M11 13V6a3 3 0 0 0-3-3H2" stroke={colors.textDim} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
      <span style={{ flex: 1, fontSize: 12, color: colors.textDim, fontFamily: fonts.mono, lineHeight: 1.4 }}>{text}</span>
      {timeStr && <span style={{ fontSize: 11, color: colors.textDim, opacity: 0.7, flexShrink: 0 }}>{timeStr}</span>}
    </div>
  );
}

function AgentActivityIndicator({ activity }: { activity: AgentActivityData }) {
  const { colors } = useTheme();
  const activityStyle = buildActivityStyle(colors);
  let icon = "&#9881;";
  let label = "";
  if (activity.activity === "model") {
    icon = "&#129302;"; // robot
    label = activity.model ?? "";
  } else if (activity.activity === "subagent_started") {
    icon = "&#128268;"; // link
    label = `Agent: ${activity.description ?? ""}`;
  } else if (activity.activity === "subagent_progress") {
    icon = "&#128269;"; // magnifying glass
    label = activity.description ?? "";
  } else if (activity.activity === "compacting") {
    icon = "&#128220;"; // scroll
    label = "Compacting context...";
  }
  if (!label) return null;
  if (label.length > 100) label = label.slice(0, 100) + "...";
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }} dangerouslySetInnerHTML={{ __html: icon }} />
      <span style={{ color: colors.textMuted }}>{label}</span>
    </div>
  );
}

function CompletionSummary({ info }: { info: { duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } }) {
  const { colors } = useTheme();
  const activityStyle = buildActivityStyle(colors);
  const parts: string[] = [];
  if (info.model) parts.push(info.model);
  if (info.duration_ms) {
    const sec = (info.duration_ms / 1000).toFixed(1);
    parts.push(`${sec}s`);
  }
  if (info.num_turns) parts.push(`${info.num_turns} turn${info.num_turns === 1 ? "" : "s"}`);
  if (info.stop_reason) parts.push(info.stop_reason);
  if (parts.length === 0) return null;
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }}>&#9201;</span>
      <span style={{ color: colors.textDim }}>{parts.join(" · ")}</span>
    </div>
  );
}

// Tools whose tool_input summary (from internal/container/runner.go's
// summarizeToolInput) is the bare file path string — render it as a single
// FileLink rather than scanning for path-shaped substrings.
const FILE_PATH_TOOLS = new Set(["Read", "Edit", "Write", "MultiEdit", "NotebookEdit", "NotebookRead"]);

function renderInputWithLinks(input: string, toolName: string, channelId: string): React.ReactNode {
  if (!input) return input;
  if (!channelId) return input;
  if (FILE_PATH_TOOLS.has(toolName)) {
    return <FileLink channelId={channelId} raw={input} line={null} />;
  }
  const candidates = findCandidatePaths(input);
  if (candidates.length === 0) return input;
  const parts: React.ReactNode[] = [];
  let last = 0;
  candidates.forEach((c, i) => {
    if (c.start > last) parts.push(input.slice(last, c.start));
    parts.push(<FileLink key={`tool-link-${i}`} channelId={channelId} raw={c.raw} line={c.line} />);
    last = c.start + c.length;
  });
  if (last < input.length) parts.push(input.slice(last));
  return <>{parts}</>;
}

function ToolActivityIndicator({ toolName, input, result }: { toolName: string; input?: string; result?: { text: string; is_error: boolean; truncated: boolean } }) {
  const { colors } = useTheme();
  const channelId = useContext(ChannelContext);
  const activityStyle = buildActivityStyle(colors);
  const [expanded, setExpanded] = useState(false);
  const safeInput = input ?? "";
  const isPathTool = FILE_PATH_TOOLS.has(toolName);
  const truncated = !isPathTool && safeInput.length > 80 ? safeInput.slice(0, 80) + "..." : safeInput;
  const fullText = result?.text ?? "";
  const previewText = fullText.length > 120 ? fullText.slice(0, 120) + "..." : fullText;
  const hasResult = fullText !== "";
  const canExpand = fullText.length > 120;
  const resultColor = result?.is_error ? colors.warning : colors.textDim;
  return (
    <div style={{ ...activityStyle, flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ opacity: 0.5 }}>&#9881;</span>
        <span style={{ color: colors.textMuted, fontWeight: 500 }}>{toolName}</span>
        {truncated && (
          <span style={{ opacity: 0.85 }}>{renderInputWithLinks(truncated, toolName, channelId)}</span>
        )}
      </div>
      {hasResult && (
        <div
          onClick={canExpand ? () => setExpanded((v) => !v) : undefined}
          style={{
            marginLeft: 22,
            opacity: 0.6,
            color: resultColor,
            cursor: canExpand ? "pointer" : "default",
            whiteSpace: expanded ? "pre-wrap" : "normal",
            wordBreak: "break-word",
            fontFamily: fonts.mono,
          }}
        >
          {expanded ? fullText : previewText}
          {result?.truncated && <span style={{ opacity: 0.5 }}> (truncated)</span>}
        </div>
      )}
    </div>
  );
}

function ThinkingBubble({ text, truncated }: { text?: string; truncated: boolean }) {
  const { colors } = useTheme();
  const [expanded, setExpanded] = useState(false);
  const safeText = text ?? "";
  const preview = safeText.length > 200 ? safeText.slice(0, 200) + "..." : safeText;
  return (
    <div
      onClick={() => setExpanded((v) => !v)}
      style={{
        marginBottom: 12,
        padding: "8px 12px",
        borderRadius: 8,
        borderLeft: `2px solid ${colors.border}`,
        backgroundColor: colors.surface,
        fontFamily: fonts.mono,
        fontSize: 12,
        color: colors.textDim,
        cursor: "pointer",
        whiteSpace: "pre-wrap",
        lineHeight: 1.5,
        opacity: 0.85,
      }}
    >
      <div style={{ fontSize: 10, fontWeight: 700, color: colors.textMuted, textTransform: "uppercase", letterSpacing: 1, marginBottom: 4 }}>
        Thinking
      </div>
      <div>{expanded ? safeText : preview}</div>
      {truncated && <div style={{ fontSize: 10, opacity: 0.5, marginTop: 4 }}>truncated</div>}
    </div>
  );
}

function TodoChecklist({ todos }: { todos: TodoItem[] }) {
  const { colors } = useTheme();
  const completed = todos.filter((t) => t.status === "completed").length;
  return (
    <div style={{ display: "flex", justifyContent: "center", padding: "4px 24px 0" }}>
      <div style={{
        width: "100%",
        maxWidth: 768,
        padding: "8px 14px",
        borderRadius: 8,
        border: `1px solid ${colors.border}`,
        backgroundColor: colors.surface,
        fontSize: 12,
        fontFamily: fonts.mono,
      }}>
        <div style={{ fontSize: 10, fontWeight: 700, color: colors.active, textTransform: "uppercase" as const, letterSpacing: 1, marginBottom: 4 }}>
          Progress {completed}/{todos.length}
        </div>
        {todos.map((todo, i) => (
          <div key={i} style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "2px 0",
            color: todo.status === "pending" ? colors.textDim
                 : todo.status === "in_progress" ? colors.active
                 : colors.text,
          }}>
            <span style={{ fontSize: 14, lineHeight: 1 }}>
              {todo.status === "completed" ? "\u2611" : "\u2610"}
            </span>
            <span style={{
              textDecoration: todo.status === "completed" ? "line-through" : "none",
              opacity: todo.status === "pending" ? 0.6 : 1,
            }}>
              {todo.status === "in_progress" ? todo.activeForm : todo.content}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function MarkdownContent({ content }: { content: string }) {
  const { colors } = useTheme();
  const channelId = useContext(ChannelContext);
  const s = buildMessageStyles(colors);
  const parts = parseMarkdown(content, s, channelId);
  return <>{parts}</>;
}

function parseMarkdown(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const lines = text.split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i] ?? "";

    // Fenced code block.
    if (line.startsWith("```")) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] ?? "").startsWith("```")) {
        codeLines.push(lines[i] ?? "");
        i++;
      }
      i++; // skip closing ```
      nodes.push(
        <pre key={nodes.length} style={s.codeBlock}>
          {lang && <div style={s.codeLang}>{lang}</div>}
          <code>{codeLines.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    // GFM table: header row + separator (|---|---|) + body rows.
    if (isTableRow(line) && i + 1 < lines.length && isTableSeparator(lines[i + 1] ?? "")) {
      const aligns = parseTableAligns(lines[i + 1] ?? "");
      const headers = splitTableRow(line);
      i += 2;
      const bodyRows: string[][] = [];
      while (i < lines.length && isTableRow(lines[i] ?? "")) {
        bodyRows.push(splitTableRow(lines[i] ?? ""));
        i++;
      }
      nodes.push(
        <table key={nodes.length} style={s.table}>
          <thead>
            <tr>
              {headers.map((h, hi) => (
                <th key={hi} style={{ ...s.tableHeaderCell, textAlign: aligns[hi] ?? "left" }}>
                  {formatInline(h, s, channelId)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {bodyRows.map((row, ri) => (
              <tr key={ri}>
                {row.map((cell, ci) => (
                  <td key={ci} style={{ ...s.tableCell, textAlign: aligns[ci] ?? "left" }}>
                    {formatInline(cell, s, channelId)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>,
      );
      continue;
    }

    // Blockquote: collect consecutive `> ` lines.
    if (line.startsWith("> ") || line === ">") {
      const quoteLines: string[] = [];
      while (i < lines.length && ((lines[i] ?? "").startsWith("> ") || (lines[i] ?? "") === ">")) {
        const ql = lines[i] ?? "";
        quoteLines.push(ql === ">" ? "" : ql.slice(2));
        i++;
      }
      nodes.push(
        <blockquote key={nodes.length} style={s.blockquote}>
          {quoteLines.map((ql, qi) => (
            <p key={qi} style={s.paragraph}>{ql ? formatInline(ql, s, channelId) : <br />}</p>
          ))}
        </blockquote>,
      );
      continue;
    }

    // Regular line — apply inline formatting.
    if (line.trim() === "") {
      nodes.push(<br key={nodes.length} />);
    } else {
      nodes.push(
        <p key={nodes.length} style={s.paragraph}>
          {formatInline(line, s, channelId)}
        </p>,
      );
    }
    i++;
  }

  return nodes;
}

function isTableRow(line: string): boolean {
  return line.includes("|") && line.trim().length > 0 && !line.trim().startsWith("```");
}

function isTableSeparator(line: string): boolean {
  // |---|:---:|---:| with optional surrounding pipes/whitespace.
  return /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(line);
}

function parseTableAligns(separator: string): ("left" | "center" | "right")[] {
  return splitTableRow(separator).map((cell) => {
    const t = cell.trim();
    const left = t.startsWith(":");
    const right = t.endsWith(":");
    if (left && right) return "center";
    if (right) return "right";
    return "left";
  });
}

function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

function linkifyText(text: string, keyBase: number, channelId: string): React.ReactNode[] {
  // Collect URL and file-path matches, then merge by start position. File-path
  // matches that overlap a URL match are dropped (URLs win — they often contain
  // a `.ext` suffix that would otherwise be mis-detected as a path).
  const urlRegex = /(https?:\/\/[^\s<>)"']+)/g;
  type Hit =
    | { kind: "url"; start: number; length: number; href: string }
    | { kind: "path"; start: number; length: number; raw: string; line: number | null };
  const hits: Hit[] = [];
  for (;;) {
    const m = urlRegex.exec(text);
    if (!m) break;
    hits.push({ kind: "url", start: m.index, length: m[0].length, href: m[0] });
  }
  if (channelId) {
    for (const c of findCandidatePaths(text)) {
      if (hits.some((h) => h.kind === "url" && c.start >= h.start && c.start < h.start + h.length)) continue;
      hits.push({ kind: "path", start: c.start, length: c.length, raw: c.raw, line: c.line });
    }
  }
  hits.sort((a, b) => a.start - b.start);

  const parts: React.ReactNode[] = [];
  let last = 0;
  for (const h of hits) {
    if (h.start < last) continue; // overlapping (shouldn't happen after URL filter, but be safe)
    if (h.start > last) parts.push(text.slice(last, h.start));
    if (h.kind === "url") {
      parts.push(
        <a
          key={`link-${keyBase}-${parts.length}`}
          href={h.href}
          target="_blank"
          rel="noopener noreferrer"
          style={{ color: "#6ba3f7", textDecoration: "underline" }}
        >
          {h.href}
        </a>,
      );
    } else {
      parts.push(
        <FileLink
          key={`file-${keyBase}-${parts.length}`}
          channelId={channelId}
          raw={h.raw}
          line={h.line}
        />,
      );
    }
    last = h.start + h.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

function formatInline(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline code, bold, italic, markdown links.
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
  let lastIndex = 0;

  for (;;) {
    const match = regex.exec(text);
    if (!match) break;

    if (match.index > lastIndex) {
      nodes.push(...linkifyText(text.slice(lastIndex, match.index), nodes.length, channelId));
    }

    const token = match[0];
    if (token.startsWith("`")) {
      nodes.push(
        <code key={nodes.length} style={s.inlineCode}>
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      nodes.push(
        <strong key={nodes.length}>{token.slice(2, -2)}</strong>,
      );
    } else if (token.startsWith("*")) {
      nodes.push(<em key={nodes.length}>{token.slice(1, -1)}</em>);
    } else if (token.startsWith("[")) {
      const mdMatch = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      if (mdMatch) {
        nodes.push(
          <a
            key={nodes.length}
            href={mdMatch[2]}
            target="_blank"
            rel="noopener noreferrer"
            style={{ color: "#6ba3f7", textDecoration: "underline" }}
          >
            {mdMatch[1]}
          </a>,
        );
      }
    }

    lastIndex = match.index + token.length;
  }

  if (lastIndex < text.length) {
    nodes.push(...linkifyText(text.slice(lastIndex), nodes.length, channelId));
  }

  return nodes;
}

// ── AskUserQuestion Card ──

function AskUserQuestionCard({ questions, channelId, mode, onSent }: { questions: AskUserQuestion[]; channelId: string; mode: "agent" | "plan"; onSent?: () => void }) {
  const { colors } = useTheme();
  const [answers, setAnswers] = useState<Map<number, string>>(new Map());
  const [otherTexts, setOtherTexts] = useState<Map<number, string>>(new Map());
  const [sending, setSending] = useState(false);

  const setAnswer = (idx: number, value: string) => {
    setAnswers((prev) => { const next = new Map(prev); next.set(idx, value); return next; });
  };

  const setOtherText = (idx: number, value: string) => {
    setOtherTexts((prev) => { const next = new Map(prev); next.set(idx, value); return next; });
  };

  const handleSend = async () => {
    setSending(true);
    const parts: string[] = [];
    for (let i = 0; i < questions.length; i++) {
      const q = questions[i]!;
      const answer = answers.get(i);
      if (!answer) continue;
      const label = q.header || q.question;
      if (answer === "__other__") {
        parts.push(`${label}: ${otherTexts.get(i) || "(no answer)"}`);
      } else {
        parts.push(`${label}: ${answer}`);
      }
    }
    const content = parts.length > 0
      ? "Here are my answers:\n" + parts.map((p) => `- ${p}`).join("\n")
      : "No specific answers provided.";
    try {
      await resolveAsk(channelId, "answer", content, mode);
      onSent?.();
    } catch { /* ignore */ }
    setSending(false);
  };

  const allAnswered = questions.every((_, i) => answers.has(i));

  return (
    <div style={{
      margin: "8px 16px",
      padding: "12px 16px",
      borderRadius: 8,
      border: `1px solid ${colors.active}`,
      backgroundColor: colors.surface,
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: colors.active, textTransform: "uppercase", letterSpacing: 1, marginBottom: 8 }}>
        Claude has questions
      </div>
      {questions.map((q, qi) => (
        <div key={qi} style={{ marginBottom: qi < questions.length - 1 ? 12 : 0 }}>
          {q.header && <div style={{ fontSize: 12, fontWeight: 600, color: colors.textLight, marginBottom: 4 }}>{q.header}</div>}
          <div style={{ fontSize: 13, color: colors.text, marginBottom: 6 }}>{q.question}</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {q.options?.map((opt) => {
              const isSelected = answers.get(qi) === opt.label;
              return (
                <button
                  key={opt.label}
                  onClick={() => setAnswer(qi, opt.label)}
                  title={opt.description}
                  style={{
                    padding: "4px 10px",
                    fontSize: 12,
                    fontFamily: fonts.mono,
                    border: `1px solid ${isSelected ? colors.active : colors.border}`,
                    borderRadius: 12,
                    backgroundColor: isSelected ? colors.active : "transparent",
                    color: isSelected ? "#fff" : colors.text,
                    cursor: "pointer",
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
            <button
              onClick={() => setAnswer(qi, "__other__")}
              style={{
                padding: "4px 10px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${answers.get(qi) === "__other__" ? colors.active : colors.border}`,
                borderRadius: 12,
                backgroundColor: answers.get(qi) === "__other__" ? colors.active : "transparent",
                color: answers.get(qi) === "__other__" ? "#fff" : colors.textDim,
                cursor: "pointer",
              }}
            >
              Other...
            </button>
          </div>
          {answers.get(qi) === "__other__" && (
            <textarea
              autoFocus
              placeholder="Type your answer (⌘/Ctrl+Enter to send)…"
              value={otherTexts.get(qi) || ""}
              onChange={(e) => setOtherText(qi, e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  if (allAnswered && !sending) void handleSend();
                }
              }}
              rows={3}
              disabled={sending}
              style={{
                marginTop: 6,
                width: "100%",
                boxSizing: "border-box",
                padding: 8,
                fontSize: 12,
                fontFamily: fonts.mono,
                backgroundColor: colors.codeBlockBg,
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                color: colors.text,
                outline: "none",
                resize: "vertical",
              }}
            />
          )}
        </div>
      ))}
      <button
        onClick={handleSend}
        disabled={!allAnswered || sending}
        style={{
          marginTop: 10,
          padding: "5px 16px",
          fontSize: 12,
          fontFamily: fonts.mono,
          border: `1px solid ${colors.active}`,
          borderRadius: 12,
          backgroundColor: allAnswered ? colors.active : "transparent",
          color: allAnswered ? "#fff" : colors.textDim,
          cursor: allAnswered ? "pointer" : "default",
          opacity: sending ? 0.5 : 1,
        }}
      >
        {sending ? "Sending..." : "Send Answers"}
      </button>
    </div>
  );
}

// ── ExitPlanMode Card ──

function ExitPlanCard({ plan, channelId, setMode, onSent }: { plan: ExitPlanModeData; channelId: string; setMode: (m: "agent" | "plan") => void; onSent?: () => void }) {
  const { colors } = useTheme();
  const [sending, setSending] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [changesOpen, setChangesOpen] = useState(false);
  const [changesText, setChangesText] = useState("");

  const handleApprove = async () => {
    setSending(true);
    try {
      await resolvePlan(channelId, "approve");
      // Flip pill only after the POST succeeds — otherwise a failed approve
      // leaves the pill on "agent" while the card still shows an unresolved plan.
      setMode("agent");
      onSent?.();
    } catch { /* ignore */ }
    setSending(false);
  };

  const handleReject = async () => {
    setSending(true);
    try {
      // ExitPlanMode auto-flipped the pill to "agent"; revert so any
      // already-queued messages stay in plan mode when the drain resumes.
      setMode("plan");
      await resolvePlan(channelId, "reject");
      onSent?.();
    } catch { /* ignore */ }
    setSending(false);
  };

  const handleRequestChanges = async () => {
    const prompt = changesText.trim();
    if (!prompt) return;
    setSending(true);
    try {
      setMode("plan");
      await resolvePlan(channelId, "deny", prompt, "plan");
      onSent?.();
    } catch { /* ignore */ }
    setSending(false);
  };

  const lines = plan.plan.split("\n");
  const preview = lines.slice(0, 5).join("\n");
  const hasMore = lines.length > 5;

  return (
    <div style={{
      margin: "8px 16px",
      padding: "12px 16px",
      borderRadius: 8,
      border: `1px solid ${colors.warning}`,
      backgroundColor: colors.surface,
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: colors.warning, textTransform: "uppercase", letterSpacing: 1, marginBottom: 8 }}>
        Plan ready for review
      </div>
      <div
        style={{
          fontSize: 12,
          fontFamily: fonts.mono,
          color: colors.text,
          whiteSpace: "pre-wrap",
          lineHeight: 1.5,
          maxHeight: expanded ? undefined : 120,
          overflow: expanded ? undefined : "hidden",
        }}
      >
        {expanded ? plan.plan : preview}
      </div>
      {hasMore && (
        <button
          onClick={() => setExpanded((v) => !v)}
          style={{
            background: "none",
            border: "none",
            color: colors.active,
            cursor: "pointer",
            fontSize: 11,
            padding: "4px 0",
            fontFamily: fonts.mono,
          }}
        >
          {expanded ? "Show less" : `Show all (${lines.length} lines)`}
        </button>
      )}
      <div style={{ display: "flex", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
        <button
          onClick={handleApprove}
          disabled={sending}
          style={{
            padding: "5px 16px",
            fontSize: 12,
            fontFamily: fonts.mono,
            border: `1px solid ${colors.active}`,
            borderRadius: 12,
            backgroundColor: colors.active,
            color: "#fff",
            cursor: "pointer",
            opacity: sending ? 0.5 : 1,
          }}
        >
          Approve & Execute
        </button>
        <button
          onClick={() => setChangesOpen((v) => !v)}
          disabled={sending}
          style={{
            padding: "5px 16px",
            fontSize: 12,
            fontFamily: fonts.mono,
            border: `1px solid ${colors.border}`,
            borderRadius: 12,
            backgroundColor: "transparent",
            color: colors.text,
            cursor: "pointer",
            opacity: sending ? 0.5 : 1,
          }}
        >
          Request Changes
        </button>
        <button
          onClick={handleReject}
          disabled={sending}
          style={{
            padding: "5px 16px",
            fontSize: 12,
            fontFamily: fonts.mono,
            border: `1px solid ${colors.border}`,
            borderRadius: 12,
            backgroundColor: "transparent",
            color: colors.text,
            cursor: "pointer",
            opacity: sending ? 0.5 : 1,
          }}
        >
          Discard
        </button>
      </div>
      {changesOpen && (
        <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 8 }}>
          <textarea
            value={changesText}
            onChange={(e) => setChangesText(e.target.value)}
            placeholder="Describe the changes you want before approving..."
            rows={3}
            style={{
              width: "100%",
              padding: "6px 8px",
              fontSize: 12,
              fontFamily: fonts.mono,
              color: colors.text,
              backgroundColor: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
              resize: "vertical",
              boxSizing: "border-box",
            }}
          />
          <div style={{ display: "flex", gap: 8 }}>
            <button
              onClick={handleRequestChanges}
              disabled={sending || !changesText.trim()}
              style={{
                padding: "5px 16px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.active}`,
                borderRadius: 12,
                backgroundColor: colors.active,
                color: "#fff",
                cursor: "pointer",
                opacity: sending || !changesText.trim() ? 0.5 : 1,
              }}
            >
              Send changes
            </button>
            <button
              onClick={() => {
                setChangesOpen(false);
                setChangesText("");
              }}
              disabled={sending}
              style={{
                padding: "5px 16px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.border}`,
                borderRadius: 12,
                backgroundColor: "transparent",
                color: colors.text,
                cursor: "pointer",
                opacity: sending ? 0.5 : 1,
              }}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

