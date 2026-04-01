import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentActivityData, AskUserQuestion, ExitPlanModeData, Message } from "../types";
import type { ChatState } from "../hooks/useChatState";
import { sendCommand, sendMessage } from "../api/loopApi";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";
import { LoopLogo } from "./LoopLogo";

// Draft text per channel — persisted to localStorage across app restarts.
const DRAFT_KEY = "loop-chat-drafts";
const draftText = {
  get(channelId: string): string | undefined {
    try {
      const drafts = JSON.parse(localStorage.getItem(DRAFT_KEY) || "{}");
      return drafts[channelId];
    } catch { return undefined; }
  },
  set(channelId: string, text: string) {
    try {
      const drafts = JSON.parse(localStorage.getItem(DRAFT_KEY) || "{}");
      drafts[channelId] = text;
      localStorage.setItem(DRAFT_KEY, JSON.stringify(drafts));
    } catch { /* ignore */ }
  },
  delete(channelId: string) {
    try {
      const drafts = JSON.parse(localStorage.getItem(DRAFT_KEY) || "{}");
      delete drafts[channelId];
      localStorage.setItem(DRAFT_KEY, JSON.stringify(drafts));
    } catch { /* ignore */ }
  },
};

function buildStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    container: {
      display: "flex",
      flexDirection: "column",
      flex: 1,
      overflow: "hidden",
    },
    welcome: {
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
      flex: 1,
      gap: 24,
      padding: 24,
    },
    welcomeContent: {
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      gap: 16,
    },
    welcomeTitle: {
      fontSize: 22,
      fontWeight: 500,
      color: colors.textLight,
    },
    messages: {
      flex: 1,
      overflowY: "auto",
      padding: "16px 24px",
    },
    messageColumn: {
      maxWidth: 768,
      margin: "0 auto",
    },
    inputBar: {
      display: "flex",
      justifyContent: "center",
      padding: "12px 24px 8px",
    },
    isolationLabel: {
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      gap: 6,
      padding: "0 0 12px",
      fontSize: 11,
      color: colors.textDim,
      fontFamily: fonts.mono,
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
    inputWrapper: {
      display: "flex",
      alignItems: "flex-end",
      gap: 8,
      width: "100%",
      maxWidth: 768,
      backgroundColor: colors.surface,
      border: `1px solid ${colors.border}`,
      borderRadius: 16,
      padding: "14px 14px 14px 18px",
    },
    textarea: {
      flex: 1,
      background: "transparent",
      border: "none",
      padding: "2px 0",
      color: colors.text,
      fontFamily: fonts.sans,
      fontSize: 14,
      lineHeight: 1.4,
      resize: "none" as const,
      outline: "none",
    },
    sendButton: {
      width: 28,
      height: 28,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      background: colors.pillActiveBg,
      border: "none",
      borderRadius: 8,
      color: colors.pillActiveText,
      cursor: "pointer",
      flexShrink: 0,
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

function buildModeStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    pill: {
      display: "flex",
      height: 28,
      borderRadius: 8,
      border: `1px solid ${colors.border}`,
      overflow: "hidden",
      flexShrink: 0,
    },
    segment: {
      padding: "0 10px",
      fontSize: 11,
      fontFamily: fonts.mono,
      cursor: "pointer",
      border: "none",
      outline: "none",
      transition: "background-color 0.2s",
      lineHeight: "28px",
    },
  };
}

function buildCommandStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    dropdown: {
      position: "absolute",
      bottom: "100%",
      left: 0,
      right: 0,
      marginBottom: 4,
      backgroundColor: colors.sidebar,
      border: `1px solid ${colors.border}`,
      borderRadius: 8,
      padding: "6px 0",
      zIndex: 10,
      maxHeight: 280,
      overflow: "hidden",
      boxShadow: `0 4px 12px ${colors.shadow}`,
    },
    scrollArea: {
      maxHeight: 268,
      overflowY: "auto",
      padding: "0 4px",
    },
    item: {
      padding: "8px 12px",
      borderRadius: 6,
      cursor: "pointer",
      display: "flex",
      flexDirection: "column" as const,
      gap: 2,
    },
    name: {
      color: colors.textLight,
      fontWeight: 600,
      fontSize: 13,
      fontFamily: fonts.mono,
    },
    desc: {
      color: colors.textMuted,
      fontSize: 12,
      fontFamily: fonts.sans,
    },
    usage: {
      color: colors.textDim,
      fontSize: 11,
      fontFamily: fonts.mono,
    },
  };
}

function buildMentionStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    dropdown: {
      position: "absolute",
      bottom: "100%",
      left: 14,
      marginBottom: 4,
      backgroundColor: colors.sidebar,
      border: `1px solid ${colors.border}`,
      borderRadius: 8,
      padding: 4,
      zIndex: 10,
      minWidth: 140,
      boxShadow: `0 4px 12px ${colors.shadow}`,
    },
    item: {
      padding: "8px 12px",
      borderRadius: 6,
      cursor: "pointer",
      backgroundColor: colors.selectedBg,
    },
    name: {
      color: colors.textLight,
      fontWeight: 600,
      fontSize: 13,
      fontFamily: fonts.sans,
    },
  };
}

interface ChatViewProps {
  channelId: string | null;
  chatState: ChatState;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
}

export function ChatView({ channelId, chatState, scrollToMessageId, onScrollComplete }: ChatViewProps) {
  const { colors, fontSizes } = useTheme();
  const styles = buildStyles(colors);
  const { messages, loading, loadMore, hasMore, streamingContent, isRunning, toolActivity, agentActivity, askUserQuestions, exitPlanRequest, completionInfo, triggerContent } = chatState;
  const dismissCards = useCallback(() => { chatState.clearAskUser(); chatState.clearExitPlan(); }, [chatState]);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);
  const [highlightedMsgId, setHighlightedMsgId] = useState<number | null>(null);

  // Auto-scroll to bottom on new messages or streaming updates.
  useEffect(() => {
    if (autoScrollRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages, streamingContent, toolActivity, agentActivity, askUserQuestions, exitPlanRequest]);

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

  const scrollToBottom = useCallback(() => {
    autoScrollRef.current = true;
    requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ behavior: "smooth" }));
  }, []);

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

  // Copy-on-select: copy selected text to clipboard on mouse up.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const onMouseUp = () => {
      const sel = window.getSelection();
      const text = sel?.toString();
      if (text && sel?.anchorNode && el.contains(sel.anchorNode)) {
        navigator.clipboard.writeText(text).catch(() => {});
      }
    };
    el.addEventListener("mouseup", onMouseUp);
    return () => el.removeEventListener("mouseup", onMouseUp);
  }, []);

  // Find the first unprocessed user message ID — the one currently being processed.
  // Later unprocessed messages are shown with a "queued" label.
  const unprocessedUserMsgs = messages.filter((m) => !m.is_bot && !m.is_processed);
  const firstUnprocessedUserMsgId = unprocessedUserMsgs[0]?.msg_id ?? null;
  const hasQueuedMessages = unprocessedUserMsgs.length > 1;

  // Track whether we ever had queued messages in this batch, so the trigger
  // quote persists even when processing the last message of a multi-message batch.
  const hadQueuedRef = useRef(false);
  if (hasQueuedMessages) hadQueuedRef.current = true;
  if (unprocessedUserMsgs.length === 0) hadQueuedRef.current = false;
  const showTriggerQuote = isRunning && !!triggerContent && (hasQueuedMessages || hadQueuedRef.current);

  const isEmpty = messages.length === 0 && !loading;

  if (!channelId) {
    return (
      <div style={styles.welcome}>
        <WelcomeScreen />
      </div>
    );
  }

  // Empty state: centered welcome + full-width input at bottom
  if (isEmpty) {
    return (
      <div style={{ ...styles.container, zoom: fontSizes.chat / 13 }}>
        <div style={styles.welcome}>
          <WelcomeScreen />
        </div>
        <div style={styles.inputBar}>
          <ChatInput channelId={channelId} messages={messages} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} />
        </div>

        <div style={styles.isolationLabel}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="7" width="20" height="14" rx="2" ry="2" />
            <polyline points="17 2 12 7 7 2" />
          </svg>
          Running non-interactively in an isolated Docker container
        </div>
      </div>
    );
  }

  return (
    <div style={{ ...styles.container, zoom: fontSizes.chat / 13 }}>
      <div ref={containerRef} style={styles.messages} onScroll={handleScroll}>
        <div style={styles.messageColumn}>
          {hasMore && (
            <button onClick={loadMore} style={styles.loadMore}>
              {loading ? "Loading..." : "Load older messages"}
            </button>
          )}
          {messages.map((msg) => (
            <MessageBubble
              key={msg.msg_id}
              message={msg}
              showProcessing={isRunning && !msg.is_bot && msg.msg_id === firstUnprocessedUserMsgId}
              showQueued={!msg.is_bot && !msg.is_processed && !(isRunning && msg.msg_id === firstUnprocessedUserMsgId)}
              highlighted={msg.id === highlightedMsgId}
            />
          ))}
          {showTriggerQuote && (
            <TriggerQuote content={triggerContent} time={firstUnprocessedUserMsgId ? messages.find((m) => m.msg_id === firstUnprocessedUserMsgId)?.created_at : undefined} />
          )}
          {isRunning && agentActivity && (
            <AgentActivityIndicator activity={agentActivity} />
          )}
          {toolActivity && !streamingContent && isRunning && (
            <ToolActivityIndicator toolName={toolActivity.tool_name} input={toolActivity.input} />
          )}
          {askUserQuestions && !isRunning && channelId && (
            <AskUserQuestionCard questions={askUserQuestions.questions} channelId={channelId} onSent={() => { chatState.clearAskUser(); scrollToBottom(); }} />
          )}
          {exitPlanRequest && !isRunning && channelId && (
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
      <div style={styles.inputBar}>
        <ChatInput channelId={channelId} messages={messages} isRunning={isRunning} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} />
      </div>
      <div style={styles.isolationLabel}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={isRunning ? colors.active : colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="2" y="7" width="20" height="14" rx="2" ry="2" />
          <polyline points="17 2 12 7 7 2" />
        </svg>
        Running non-interactively in an isolated Docker container
      </div>
    </div>
  );
}

function WelcomeScreen() {
  return (
    <div style={{ display: "flex", flexDirection: "column" as const, alignItems: "center", gap: 16 }}>
      <LoopLogo />
    </div>
  );
}

function MessageBubble({ message, showProcessing, showQueued, highlighted }: { message: Message; showProcessing?: boolean; showQueued?: boolean; highlighted?: boolean }) {
  const { colors } = useTheme();
  const styles = buildStyles(colors);
  const isUser = !message.is_bot;
  const time = new Date(message.created_at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });

  return (
    <div
      data-msg-id={message.id}
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
            {showQueued && <span style={{ fontSize: 10, color: colors.textDim, background: colors.surface, border: "1px solid rgba(255,255,255,0.2)", borderRadius: 4, padding: "2px 8px", fontWeight: 500, letterSpacing: 0.3 }}>queued</span>}
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
  const styles = buildStyles(colors);
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

function ToolActivityIndicator({ toolName, input }: { toolName: string; input: string }) {
  const { colors } = useTheme();
  const activityStyle = buildActivityStyle(colors);
  const summary = input.length > 80 ? input.slice(0, 80) + "..." : input;
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }}>&#9881;</span>
      <span style={{ color: colors.textMuted, fontWeight: 500 }}>{toolName}</span>
      {summary && <span style={{ opacity: 0.7 }}>{summary}</span>}
    </div>
  );
}

function MarkdownContent({ content }: { content: string }) {
  const { colors } = useTheme();
  const s = buildStyles(colors);
  const parts = parseMarkdown(content, s);
  return <>{parts}</>;
}

function parseMarkdown(text: string, s: Record<string, React.CSSProperties>): React.ReactNode[] {
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

    // Regular line — apply inline formatting.
    if (line.trim() === "") {
      nodes.push(<br key={nodes.length} />);
    } else {
      nodes.push(
        <p key={nodes.length} style={s.paragraph}>
          {formatInline(line, s)}
        </p>,
      );
    }
    i++;
  }

  return nodes;
}

function linkifyText(text: string, keyBase: number): React.ReactNode[] {
  const urlRegex = /(https?:\/\/[^\s<>)"']+)/g;
  const parts: React.ReactNode[] = [];
  let last = 0;
  for (;;) {
    const m = urlRegex.exec(text);
    if (!m) break;
    if (m.index > last) parts.push(text.slice(last, m.index));
    parts.push(
      <a
        key={`link-${keyBase}-${parts.length}`}
        href={m[0]}
        target="_blank"
        rel="noopener noreferrer"
        style={{ color: "#6ba3f7", textDecoration: "underline" }}
      >
        {m[0]}
      </a>,
    );
    last = m.index + m[0].length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

function formatInline(text: string, s: Record<string, React.CSSProperties>): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline code, bold, italic, markdown links.
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
  let lastIndex = 0;

  for (;;) {
    const match = regex.exec(text);
    if (!match) break;

    if (match.index > lastIndex) {
      nodes.push(...linkifyText(text.slice(lastIndex, match.index), nodes.length));
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
    nodes.push(...linkifyText(text.slice(lastIndex), nodes.length));
  }

  return nodes;
}

interface CommandDef {
  name: string;
  description: string;
  usage?: string;
}

const LOOP_COMMANDS: CommandDef[] = [
  { name: "tasks", description: "List scheduled tasks" },
  { name: "task", description: "Show task details", usage: "<task_id>" },
  { name: "schedule", description: "Schedule a new task", usage: 'type=<cron|once|interval> schedule="<expr>" prompt="<text>"' },
  { name: "cancel", description: "Cancel a task", usage: "<task_id>" },
  { name: "toggle", description: "Enable/disable a task", usage: "<task_id>" },
  { name: "edit", description: "Edit a task", usage: '<task_id> [schedule="..."] [type=...] [prompt="..."]' },
  { name: "status", description: "Check bot status" },
  { name: "stop", description: "Stop active run", usage: "[channel_id]" },
  { name: "readme", description: "Show README" },
  { name: "template-add", description: "Add a template", usage: "<name>" },
  { name: "template-list", description: "List templates" },
  { name: "allow_user", description: "Grant user access", usage: "<user_id> [owner|member]" },
  { name: "deny_user", description: "Revoke user access", usage: "<user_id>" },
  { name: "iamtheowner", description: "Claim channel ownership" },
];

function ChatInput({ channelId, messages, isRunning, mode, setMode, onDismissCards, onSent }: { channelId: string; messages: Message[]; isRunning?: boolean; mode: "agent" | "plan"; setMode: (m: "agent" | "plan") => void; onDismissCards?: () => void; onSent?: () => void }) {
  const { colors } = useTheme();
  const styles = buildStyles(colors);
  const modeStyles = buildModeStyles(colors);
  const commandStyles = buildCommandStyles(colors);
  const mentionStyles = buildMentionStyles(colors);
  const [text, setText] = useState(() => draftText.get(channelId) ?? "");
  const [sending, setSending] = useState(false);
  const [showMention, setShowMention] = useState(false);
  const [mentionIdx, setMentionIdx] = useState(-1);
  const [showCommands, setShowCommands] = useState(false);
  const [filteredCommands, setFilteredCommands] = useState<CommandDef[]>([]);
  const [cmdSelectedIdx, setCmdSelectedIdx] = useState(0);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const cmdDropdownRef = useRef<HTMLDivElement>(null);

  // ── Message history (ArrowUp / ArrowDown) ──
  // Stores user-sent message contents for this channel; -1 = composing new text.
  const historyRef = useRef<string[]>([]);
  const historyIdxRef = useRef(-1);
  const draftRef = useRef("");
  const historyChannelRef = useRef<string | null>(null);

  // Keep history scoped to the active channel and clear stale entries on switch.
  useEffect(() => {
    const channelChanged = historyChannelRef.current !== channelId;
    historyChannelRef.current = channelId;

    const userMsgs = messages.filter((m) => !m.is_bot).map((m) => m.content);
    historyRef.current = userMsgs;
    if (channelChanged) {
      historyIdxRef.current = -1;
      draftRef.current = draftText.get(channelId) ?? "";
    }
  }, [channelId, messages]);

  // Auto-focus textarea on mount; move cursor to end if restoring a draft.
  useEffect(() => {
    const el = inputRef.current;
    if (el) {
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, []);

  // Scroll selected command item into view.
  useEffect(() => {
    const container = cmdDropdownRef.current;
    if (!container || !showCommands) return;
    const item = container.children[cmdSelectedIdx] as HTMLElement | undefined;
    item?.scrollIntoView({ block: "nearest" });
  }, [cmdSelectedIdx, showCommands]);

  const isLoopCommand = useCallback((t: string) => t.trimStart().startsWith("/loop"), []);

  const handleStop = useCallback(async () => {
    await sendCommand(channelId, "stop");
  }, [channelId]);

  const handleSend = useCallback(async () => {
    const trimmed = text.trim();
    if (!trimmed || sending) return;
    setSending(true);
    try {
      if (isLoopCommand(trimmed)) {
        const cmdText = trimmed.replace(/^\/loop\s*/, "");
        if (cmdText) {
          await sendCommand(channelId, cmdText);
        }
      } else {
        await sendMessage(channelId, trimmed, mode);
      }
      // Push to history.
      historyRef.current.push(trimmed);
      historyIdxRef.current = -1;
      draftRef.current = "";

      setText("");
      draftText.delete(channelId);
      onDismissCards?.();
      onSent?.();
    } finally {
      setSending(false);
      // Re-focus after React re-enables the textarea on the next render.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [channelId, text, sending, mode, isLoopCommand, onDismissCards]);

  const updateCommandDropdown = useCallback((val: string) => {
    const trimmed = val.trimStart();
    if (!trimmed.startsWith("/")) {
      setShowCommands(false);
      return;
    }
    // Match "/loop" prefix (partial or full).
    const loopPrefix = "/loop";
    if (trimmed.length <= loopPrefix.length) {
      if (loopPrefix.startsWith(trimmed)) {
        setFilteredCommands(LOOP_COMMANDS);
        setCmdSelectedIdx(0);
        setShowCommands(true);
      } else {
        setShowCommands(false);
      }
      return;
    }
    if (!trimmed.startsWith("/loop ")) {
      setShowCommands(false);
      return;
    }
    // Filter subcommands by partial match.
    const afterLoop = trimmed.slice(6);
    if (afterLoop.includes(" ")) {
      // Already has a subcommand + args — hide picker.
      setShowCommands(false);
      return;
    }
    const matches = LOOP_COMMANDS.filter((c) =>
      c.name.startsWith(afterLoop.toLowerCase()),
    );
    setFilteredCommands(matches);
    setCmdSelectedIdx(0);
    setShowCommands(matches.length > 0);
  }, []);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const val = e.target.value;
      setText(val);
      if (val) draftText.set(channelId, val);
      else draftText.delete(channelId);

      // Check for command autocomplete.
      updateCommandDropdown(val);

      // Check for @mention autocomplete.
      const pos = e.target.selectionStart;
      const before = val.slice(0, pos);
      const atIdx = before.lastIndexOf("@");
      if (atIdx !== -1 && (atIdx === 0 || before[atIdx - 1] === " " || before[atIdx - 1] === "\n")) {
        const partial = before.slice(atIdx + 1);
        if ("LoopBot".toLowerCase().startsWith(partial.toLowerCase()) && !partial.includes(" ")) {
          setShowMention(true);
          setMentionIdx(atIdx);
          return;
        }
      }
      setShowMention(false);
    },
    [updateCommandDropdown],
  );

  const acceptCommand = useCallback((cmd: CommandDef) => {
    const newText = `/loop ${cmd.name} `;
    setText(newText);
    draftText.set(channelId, newText);
    setShowCommands(false);
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el) {
        el.focus();
        el.setSelectionRange(newText.length, newText.length);
      }
    });
  }, []);

  const acceptMention = useCallback(() => {
    if (mentionIdx < 0) return;
    const pos = inputRef.current?.selectionStart ?? text.length;
    const newText = text.slice(0, mentionIdx) + "@LoopBot " + text.slice(pos);
    setText(newText);
    draftText.set(channelId, newText);
    setShowMention(false);
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el) {
        const cursorPos = mentionIdx + "@LoopBot ".length;
        el.focus();
        el.setSelectionRange(cursorPos, cursorPos);
      }
    });
  }, [mentionIdx, text]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      // Command picker navigation.
      if (showCommands && filteredCommands.length > 0) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setCmdSelectedIdx((i) => Math.min(i + 1, filteredCommands.length - 1));
          return;
        }
        if (e.key === "ArrowUp") {
          e.preventDefault();
          setCmdSelectedIdx((i) => Math.max(i - 1, 0));
          return;
        }
        if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
          e.preventDefault();
          const cmd = filteredCommands[cmdSelectedIdx];
          if (cmd) acceptCommand(cmd);
          return;
        }
        if (e.key === "Escape") {
          setShowCommands(false);
          return;
        }
      }
      // Mention picker.
      if (showMention && (e.key === "Tab" || e.key === "Enter")) {
        e.preventDefault();
        acceptMention();
        return;
      }
      if (e.key === "Escape" && showMention) {
        setShowMention(false);
        return;
      }
      // Message history navigation.
      if (e.key === "ArrowUp" && !showCommands && !showMention) {
        const el = inputRef.current;
        if (el && el.selectionStart === 0 && el.selectionEnd === 0) {
          const hist = historyRef.current;
          if (hist.length === 0) return;
          e.preventDefault();
          if (historyIdxRef.current === -1) {
            // Save current draft before entering history.
            draftRef.current = text;
            historyIdxRef.current = hist.length - 1;
          } else if (historyIdxRef.current > 0) {
            historyIdxRef.current--;
          }
          const val = hist[historyIdxRef.current]!;
          setText(val);
          draftText.set(channelId, val);
          requestAnimationFrame(() => {
            if (el) el.setSelectionRange(0, 0);
          });
          return;
        }
      }
      if (e.key === "ArrowDown" && !showCommands && !showMention) {
        const el = inputRef.current;
        if (el && el.selectionStart === el.value.length && el.selectionEnd === el.value.length && historyIdxRef.current !== -1) {
          e.preventDefault();
          const hist = historyRef.current;
          if (historyIdxRef.current < hist.length - 1) {
            historyIdxRef.current++;
            const val = hist[historyIdxRef.current]!;
            setText(val);
            draftText.set(channelId, val);
          } else {
            // Back to draft.
            historyIdxRef.current = -1;
            const val = draftRef.current;
            setText(val);
            if (val) draftText.set(channelId, val);
            else draftText.delete(channelId);
          }
          requestAnimationFrame(() => {
            if (el) el.setSelectionRange(el.value.length, el.value.length);
          });
          return;
        }
      }
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSend();
      }
    },
    [handleSend, text, channelId, showMention, acceptMention, showCommands, filteredCommands, cmdSelectedIdx, acceptCommand],
  );

  return (
    <div style={{ position: "relative", ...styles.inputWrapper }}>
      {showCommands && filteredCommands.length > 0 && (
        <div style={commandStyles.dropdown}>
          <div ref={cmdDropdownRef} style={commandStyles.scrollArea}>
            {filteredCommands.map((cmd, i) => (
              <div
                key={cmd.name}
                style={{
                  ...commandStyles.item,
                  backgroundColor: i === cmdSelectedIdx ? colors.selectedBg : "transparent",
                }}
                onMouseDown={(e) => { e.preventDefault(); acceptCommand(cmd); }}
                onMouseEnter={() => setCmdSelectedIdx(i)}
              >
                <div style={commandStyles.name}>/{cmd.name}</div>
                <div style={commandStyles.desc}>{cmd.description}</div>
                {cmd.usage && <div style={commandStyles.usage}>{cmd.usage}</div>}
              </div>
            ))}
          </div>
        </div>
      )}
      {showMention && (
        <div style={mentionStyles.dropdown} onMouseDown={(e) => { e.preventDefault(); acceptMention(); }}>
          <div style={mentionStyles.item}>
            <span style={mentionStyles.name}>@LoopBot</span>
          </div>
        </div>
      )}
      <textarea
        ref={inputRef}
        style={styles.textarea}
        value={text}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        placeholder="Ask Loop anything, / for commands"
        rows={3}
        disabled={sending}
      />
      <div style={modeStyles.pill}>
        <button
          style={{
            ...modeStyles.segment,
            backgroundColor: mode === "agent" ? colors.pillActiveBg : "transparent",
            color: mode === "agent" ? colors.pillActiveText : colors.textDim,
          }}
          onClick={() => setMode("agent")}
        >
          Agent
        </button>
        <button
          style={{
            ...modeStyles.segment,
            backgroundColor: mode === "plan" ? colors.pillActiveBg : "transparent",
            color: mode === "plan" ? colors.pillActiveText : colors.textDim,
          }}
          onClick={() => setMode("plan")}
        >
          Plan
        </button>
      </div>
      {isRunning ? (
        <button
          style={{ ...styles.sendButton, background: "transparent", border: `1px solid ${colors.textDim}`, color: colors.textDim }}
          onClick={handleStop}
          title="Stop"
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
            <rect width="10" height="10" rx="2" fill="currentColor"/>
          </svg>
        </button>
      ) : (
        <button
          style={{
            ...styles.sendButton,
            opacity: text.trim() && !sending ? 1 : 0.4,
          }}
          onClick={handleSend}
          disabled={!text.trim() || sending}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
            <path d="M8 14V2M8 2L3 7M8 2L13 7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        </button>
      )}
    </div>
  );
}

// ── AskUserQuestion Card ──

function AskUserQuestionCard({ questions, channelId, onSent }: { questions: AskUserQuestion[]; channelId: string; onSent?: () => void }) {
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
      await sendMessage(channelId, content);
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
            <input
              autoFocus
              placeholder="Type your answer..."
              value={otherTexts.get(qi) || ""}
              onChange={(e) => setOtherText(qi, e.target.value)}
              style={{
                marginTop: 6,
                width: "100%",
                boxSizing: "border-box",
                padding: "4px 8px",
                fontSize: 12,
                fontFamily: fonts.mono,
                backgroundColor: colors.bg,
                border: `1px solid ${colors.border}`,
                borderRadius: 4,
                color: colors.textLight,
                outline: "none",
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

  const handleAccept = async () => {
    setSending(true);
    try {
      setMode("agent");
      await sendMessage(channelId, "I approve the plan. Please proceed with the implementation.");
      onSent?.();
    } catch { /* ignore */ }
    setSending(false);
  };

  const handleReject = async () => {
    setSending(true);
    try {
      await sendMessage(channelId, "I'd like changes to the plan. Let's discuss.", "plan");
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
      <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
        <button
          onClick={handleAccept}
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
          Accept & Execute
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
          Request Changes
        </button>
      </div>
    </div>
  );
}
