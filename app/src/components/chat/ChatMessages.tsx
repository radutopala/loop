import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import type { Message, TimelineItem } from "../../types";
import type { ChatState } from "../../hooks/useChatState";
import { useTheme } from "../../ThemeContext";
import { QueuedMessagesPopup } from "./QueuedMessagesPopup";
import { ApprovalCard } from "./ApprovalCard";
import { orderTimelineItems } from "./orderTimelineItems";
import { ChannelContext, buildMessageStyles } from "./chatShared";
import {
  MessageBubble,
  StreamingBubble,
  ToolRunBlock,
  TaskChecklist,
  TriggerQuote,
  AgentActivityIndicator,
  CompletionSummary,
  AskUserQuestionCard,
  ExitPlanCard,
  renderTimelineItem,
} from "./bubbles";

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
  const { items, liveTail, messages, loading, loadMore, hasMore, streamingContent, isRunning, agentActivity, askUserQuestions, exitPlanRequest, agentTasks, completionInfo, triggerContent, gateApprovals, processingMsgId, queuedMessages: backendQueue } = chatState;
  // The approval card belongs to chat only when the gate is attributed to the
  // chat agent. Terminal-sourced gates ("terminal:<leafId>") render in the
  // matching terminal pane instead.
  const chatGateApproval = gateApprovals["chat"] ?? null;
  // Pair tool_use with its tool_result by tool_use_id so the renderer can
  // collapse them into a single pill with output. Skip pairing when the
  // tool_use_id is empty (live events without a stable id).
  const allItems: TimelineItem[] = orderTimelineItems([...items, ...liveTail]);
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
    if (!autoScrollRef.current) return;
    // Jump straight to the true scroll bottom (no smooth animation). While a reply
    // streams, the content keeps growing, so a smooth scroll — which animates
    // toward the height captured when it started — settles ABOVE the new bottom.
    // That both leaves the latest text below the fold and trips handleScroll's
    // "at bottom?" check, flipping auto-follow off until the user manually scrolls
    // down. An instant pin to scrollHeight always tracks the growing content (and
    // keeps interactive cards' action buttons in view).
    const el = containerRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    // Re-pin not just on message/stream growth but whenever the bottom region
    // changes height: the queue (backendQueue) gaining/losing items, a run
    // starting/finishing (isRunning) which mounts the processing indicator and
    // the sticky "currently running" quote banner, the quote's source/content
    // (processingMsgId/triggerContent), or the completion summary. Without these,
    // those elements appear below the fold and the view stays put.
  }, [messages, items, liveTail, streamingContent, agentActivity, askUserQuestions, exitPlanRequest, agentTasks, chatGateApproval, isRunning, processingMsgId, backendQueue, triggerContent, completionInfo]);

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

  // Canonical queue and processing pointer both come from the backend:
  //   - `processingMsgId` is set by agent.status (running) and cleared on
  //     completion / messages.processed for that id.
  //   - `backendQueue` is the GET /api/channels/{id}/queued response, already
  //     ordered by (priority DESC, id ASC). It includes the in-flight row so
  //     we filter it out for the "waiting behind" list.
  // Fallback: during the brief startup window before the first agent.status
  // event arrives, use the first backend-queued message as the processing one.
  const effectiveProcessingMsgId =
    processingMsgId ?? (isRunning ? backendQueue[0]?.msg_id ?? null : null);
  // Messages waiting behind the currently-processing one; deletable from the popup.
  const queuedMessages = backendQueue.filter((m) => m.msg_id !== effectiveProcessingMsgId);

  // Queue position labels ("1/3" etc.) keyed by msg_id. Backend order is
  // already (priority DESC, id ASC), so positions are simply 1-indexed.
  const queuePositionByMsgId = new Map<string, string>();
  queuedMessages.forEach((m, idx) => {
    queuePositionByMsgId.set(m.msg_id, `${idx + 1}/${queuedMessages.length}`);
  });
  // Set of msg_ids the backend considers queued. Used to drive the per-bubble
  // "queued" label so it stays synced with the popup even when the locally
  // loaded message's is_processed field is stale.
  const queuedMsgIdSet = new Set(queuedMessages.map((m) => m.msg_id));

  // Track the viewport status (above / visible / below) of every user
  // message in the chat. "visible" means the user can still read the
  // message header — i.e. the bubble's top edge is inside the viewport.
  // A long message that's scrolled so far up that only its trailing lines
  // remain is "above" because the content (which lives at the top) is
  // gone. IntersectionObserver can't reliably fire on this top-edge
  // crossing without changing the intersection ratio, so we recompute
  // states on every scroll/resize via rAF instead.
  const [userMsgStates, setUserMsgStates] = useState<Map<string, "above" | "visible" | "below">>(new Map());
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let rafId = 0;
    const recompute = () => {
      rafId = 0;
      const rect = container.getBoundingClientRect();
      const nodes = container.querySelectorAll<HTMLElement>('[data-msg-uuid][data-is-user="true"]');
      setUserMsgStates((prev) => {
        const next = new Map<string, "above" | "visible" | "below">();
        nodes.forEach((n) => {
          const id = n.dataset.msgUuid;
          if (!id) return;
          const r = n.getBoundingClientRect();
          if (r.top < rect.top) next.set(id, "above");
          else if (r.top > rect.bottom) next.set(id, "below");
          else next.set(id, "visible");
        });
        if (prev.size === next.size) {
          let same = true;
          for (const [k, v] of next) {
            if (prev.get(k) !== v) { same = false; break; }
          }
          if (same) return prev;
        }
        return next;
      });
    };
    const schedule = () => {
      if (rafId) return;
      rafId = requestAnimationFrame(recompute);
    };
    recompute();
    container.addEventListener("scroll", schedule, { passive: true });
    const ro = new ResizeObserver(schedule);
    ro.observe(container);
    return () => {
      container.removeEventListener("scroll", schedule);
      ro.disconnect();
      if (rafId) cancelAnimationFrame(rafId);
    };
  }, [messages, items, liveTail]);

  // Decide which user message (if any) to quote in the floating banner.
  // - "bottom": the in-flight run's triggering message when it's scrolled
  //   away — pins to the bottom near the live tail so the user keeps
  //   context about what's currently running.
  // - "top": when no run is in flight and no user message is visible at
  //   all (parked deep in a stretch of bot output), surface the most
  //   recent user message above the viewport, pinned to the top so it
  //   reads as the prompt the visible content is replying to.
  const userMessages = useMemo(() => messages.filter((m) => !m.is_bot), [messages]);
  const quoteAnchor: { msgId: string; content: string; time?: string; position: "top" | "bottom" } | null = (() => {
    if (isRunning && effectiveProcessingMsgId && userMsgStates.get(effectiveProcessingMsgId) !== "visible") {
      // userMessages only spans the locally loaded window; for runs whose
      // trigger row predates the loaded pages, the backend queue (which
      // includes the in-flight row) still has the full Message.
      const m = userMessages.find((u) => u.msg_id === effectiveProcessingMsgId)
        ?? backendQueue.find((b) => b.msg_id === effectiveProcessingMsgId);
      const content = triggerContent ?? m?.content ?? "";
      if (!content) return null;
      return {
        msgId: effectiveProcessingMsgId,
        content,
        time: m?.created_at,
        position: "bottom",
      };
    }
    const anyVisible = userMessages.some((m) => userMsgStates.get(m.msg_id) === "visible");
    if (!anyVisible) {
      for (let i = userMessages.length - 1; i >= 0; i--) {
        const m = userMessages[i]!;
        if (userMsgStates.get(m.msg_id) === "above" && m.content) {
          return { msgId: m.msg_id, content: m.content, time: m.created_at, position: "top" };
        }
      }
    }
    return null;
  })();

  return (
    <ChannelContext.Provider value={channelId}>
      <div ref={containerRef} style={styles.messages} onScroll={handleScroll}>
        {quoteAnchor?.position === "top" && (
          <div style={{ position: "sticky", top: 0, zIndex: 2, paddingBottom: 4, backgroundColor: "transparent" }}>
            <div style={{ maxWidth: 768, margin: "0 auto" }}>
              <TriggerQuote
                content={quoteAnchor.content}
                time={quoteAnchor.time}
                onClick={() => {
                  const target = containerRef.current?.querySelector(`[data-msg-uuid="${quoteAnchor.msgId}"]`);
                  target?.scrollIntoView({ behavior: "smooth", block: "center" });
                }}
              />
            </div>
          </div>
        )}
        <div style={styles.messageColumn}>
          {hasMore && (
            <button onClick={loadMore} style={styles.loadMore}>
              {loading ? "Loading..." : "Load older messages"}
            </button>
          )}
          {(() => {
            const groups = groupTimelineItems(visibleAllItems, queuedMsgIdSet);
            const lastIdx = groups.length - 1;
            return groups.map((g, idx) => {
              if (g.kind === "message") {
                const msg = g.data.data;
                return (
                  <MessageBubble
                    key={`m-${msg.msg_id}`}
                    message={msg}
                    showProcessing={isRunning && !msg.is_bot && msg.msg_id === effectiveProcessingMsgId}
                    showQueued={!msg.is_bot && queuedMsgIdSet.has(msg.msg_id)}
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
          {isRunning && agentActivity && (
            <AgentActivityIndicator activity={agentActivity} />
          )}
          {chatGateApproval && (
            <ApprovalCard data={chatGateApproval} channelId={channelId} onResolved={() => { chatState.clearGateApproval("chat"); scrollToBottom(); }} />
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
        {quoteAnchor?.position === "bottom" && (
          <div style={{ position: "sticky", bottom: 0, zIndex: 2, paddingTop: 4 }}>
            <div style={{ maxWidth: 768, margin: "0 auto" }}>
              <TriggerQuote
                content={quoteAnchor.content}
                time={quoteAnchor.time}
                onClick={() => {
                  const target = containerRef.current?.querySelector(`[data-msg-uuid="${quoteAnchor.msgId}"]`);
                  target?.scrollIntoView({ behavior: "smooth", block: "center" });
                }}
              />
            </div>
          </div>
        )}
      </div>
      {agentTasks && agentTasks.tasks.length > 0 && (
        <TaskChecklist tasks={agentTasks.tasks} />
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

// groupTimelineItems renders user messages together with the bot replies and
// agent events their run produced (matched by trigger_msg_id). This survives
// out-of-order processing: when a priority-bumped message runs ahead of older
// queued ones, its events still group under it instead of attaching to a
// neighbouring user row by array position. Orphans (events whose trigger isn't
// in the current window, or pre-feature rows without trigger_msg_id) fall
// through to positional grouping so reload of an older page still renders.
//
// Queued user messages act as routing boundaries: when a still-running prior
// trigger keeps emitting events after the user has queued a new message, those
// later events stop routing back under the prior trigger and fall through to
// positional grouping. Without this, the prior trigger's growing event list
// would keep pushing the queued message visually further down even though its
// chain_position is fixed.
// orderTimelineItems lives in ./orderTimelineItems so it can be unit-tested
// without pulling in this component's React/DOM dependency tree.

function groupTimelineItems(items: TimelineItem[], queuedMsgIdSet: Set<string>): TimelineGroup[] {
  const presentUserMsgIds = new Set<string>();
  const userIdxByMsgId = new Map<string, number>();
  const queuedIndices: number[] = [];
  for (let i = 0; i < items.length; i++) {
    const it = items[i]!;
    if (it.kind === "message" && !it.data.is_bot) {
      presentUserMsgIds.add(it.data.msg_id);
      userIdxByMsgId.set(it.data.msg_id, i);
      if (queuedMsgIdSet.has(it.data.msg_id)) queuedIndices.push(i);
    }
  }
  const byTrigger = new Map<string, TimelineItem[]>();
  const isRouted = new Set<TimelineItem>();
  for (let i = 0; i < items.length; i++) {
    const it = items[i]!;
    const trig = it.trigger_msg_id;
    if (!trig || !presentUserMsgIds.has(trig)) continue;
    const triggerIdx = userIdxByMsgId.get(trig)!;
    let blocked = false;
    for (const qi of queuedIndices) {
      if (qi <= triggerIdx) continue;
      if (qi < i) { blocked = true; }
      break;
    }
    if (blocked) continue;
    const arr = byTrigger.get(trig) ?? [];
    arr.push(it);
    byTrigger.set(trig, arr);
    isRouted.add(it);
  }

  const out: TimelineGroup[] = [];
  let bucket: TimelineItem[] = [];
  const flushBucket = () => {
    if (bucket.length) {
      out.push({ kind: "agent", items: bucket });
      bucket = [];
    }
  };
  const emitTriggered = (userMsgId: string) => {
    const triggered = byTrigger.get(userMsgId);
    if (!triggered) return;
    let agentBucket: TimelineItem[] = [];
    const flushAgent = () => {
      if (agentBucket.length) {
        out.push({ kind: "agent", items: agentBucket });
        agentBucket = [];
      }
    };
    for (const t of triggered) {
      if (t.kind === "message") {
        flushAgent();
        out.push({ kind: "message", data: t });
      } else {
        agentBucket.push(t);
      }
    }
    flushAgent();
  };
  for (const it of items) {
    if (isRouted.has(it)) continue;
    if (it.kind === "message") {
      flushBucket();
      out.push({ kind: "message", data: it });
      if (!it.data.is_bot) emitTriggered(it.data.msg_id);
    } else {
      bucket.push(it);
    }
  }
  flushBucket();
  return out;
}
