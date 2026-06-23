import { useCallback, useContext, useEffect, useRef, useState } from "react";
import type { AgentActivityData, AskUserQuestion, ExitPlanModeData, Message, TaskItem, TimelineItem } from "../../types";
import { resolveAsk, resolvePlan } from "../../api/channels";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { ContextMenu } from "../shared/ContextMenu";
import type { MenuItem } from "../shared/ContextMenu";
import {
  ChannelContext,
  buildMessageStyles,
  buildActivityStyle,
  FILE_PATH_TOOLS,
  renderInputWithLinks,
} from "./chatShared";
import { MarkdownContent } from "./markdown";

export function CompactingMarker() {
  const { colors } = useTheme();
  const activityStyle = buildActivityStyle(colors);
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }} dangerouslySetInnerHTML={{ __html: "&#128220;" }} />
      <span style={{ color: colors.textMuted }}>Compacted context</span>
    </div>
  );
}

export function ToolRunBlock({ items, resultsByToolUseID, skippedToolResultIDs, isActive }: {
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

export function MessageBubble({ message, showProcessing, showQueued, queuePosition, highlighted, onQuote }: { message: Message; showProcessing?: boolean; showQueued?: boolean; queuePosition?: string; highlighted?: boolean; onQuote?: (msg: Message) => void }) {
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
      data-msg-uuid={message.msg_id}
      data-is-user={isUser ? "true" : undefined}
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
          // Cap user bubbles so the right-aligned colored pill doesn't
          // stretch the full column, but let assistant bubbles fill it so
          // the message-column right edge matches the chat input below.
          maxWidth: isUser ? "85%" : "100%",
          width: isUser ? undefined : "100%",
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

export function StreamingBubble({ content }: { content: string }) {
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
          width: "100%",
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

export function TriggerQuote({ content, time, onClick }: { content: string; time?: string; onClick?: () => void }) {
  const { colors } = useTheme();
  const text = content.length > 120 ? content.slice(0, 120) + "…" : content;
  const timeStr = time ? new Date(time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : null;
  return (
    <div
      data-testid="trigger-quote"
      onClick={onClick}
      title={onClick ? "Jump to message" : undefined}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "6px 12px",
        borderRadius: 6,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.border}`,
        boxShadow: "0 1px 3px rgba(0,0,0,0.08)",
        cursor: onClick ? "pointer" : "default",
      }}
    >
      <svg width="12" height="12" viewBox="0 0 16 16" fill="none" style={{ flexShrink: 0, opacity: 0.5 }}>
        <path d="M14 10l-3 3-3-3" stroke={colors.textDim} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        <path d="M11 13V6a3 3 0 0 0-3-3H2" stroke={colors.textDim} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
      <span style={{ flex: 1, fontSize: 12, color: colors.textDim, fontFamily: fonts.mono, lineHeight: 1.4 }}>{text}</span>
      {timeStr && <span style={{ fontSize: 11, color: colors.textDim, opacity: 0.7, flexShrink: 0 }}>{timeStr}</span>}
    </div>
  );
}

export function AgentActivityIndicator({ activity }: { activity: AgentActivityData }) {
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
  } else if (activity.activity === "thinking") {
    icon = "&#129504;"; // brain
    const toks = activity.description ?? "";
    label = toks && toks !== "0" ? `Thinking… (${toks} tokens)` : "Thinking…";
  } else if (activity.activity === "rate_limited") {
    icon = "&#9203;"; // hourglass
    label = activity.description ?? "Rate limited — retrying…";
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

export function CompletionSummary({ info }: { info: { duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } }) {
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

export function ToolActivityIndicator({ toolName, input, result }: { toolName: string; input?: string; result?: { text: string; is_error: boolean; truncated: boolean } }) {
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

export function ThinkingBubble({ text, truncated }: { text?: string; truncated: boolean }) {
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

export function TaskChecklist({ tasks }: { tasks: TaskItem[] }) {
  const { colors } = useTheme();
  const completed = tasks.filter((t) => t.status === "completed").length;
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
          Tasks {completed}/{tasks.length}
        </div>
        {tasks.map((task) => (
          <div key={task.id} style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "2px 0",
            color: task.status === "pending" ? colors.textDim
                 : task.status === "in_progress" ? colors.active
                 : colors.text,
          }}>
            <span style={{ fontSize: 14, lineHeight: 1 }}>
              {task.status === "completed" ? "☑" : "☐"}
            </span>
            <span style={{
              textDecoration: task.status === "completed" ? "line-through" : "none",
              opacity: task.status === "pending" ? 0.6 : 1,
            }}>
              {task.status === "in_progress" && task.activeForm ? task.activeForm : task.subject}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── AskUserQuestion Card ──

export function AskUserQuestionCard({ questions, channelId, mode, onSent }: { questions: AskUserQuestion[]; channelId: string; mode: "agent" | "plan"; onSent?: () => void }) {
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
      const answerText = answer === "__other__" ? (otherTexts.get(i) || "(no answer)") : answer;
      // Pair each answer with its full question. The agent may pick these up in a
      // fresh turn (or a different worktree) where it no longer has the question
      // in context, so the short header alone would be ambiguous.
      parts.push(`Q: ${q.question}\nA: ${answerText}`);
    }
    const content = parts.length > 0
      ? "Here are my answers:\n\n" + parts.join("\n\n")
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

export function ExitPlanCard({ plan, channelId, setMode, onSent }: { plan: ExitPlanModeData; channelId: string; setMode: (m: "agent" | "plan") => void; onSent?: () => void }) {
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

export function renderTimelineItem(
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
