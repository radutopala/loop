import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentActivityData, AgentStatusData, Message, MessageCreatedData, MessageStreamingData, ToolUseData, WSEvent } from "../types";
import { useMessages } from "../hooks/useMessages";
import { useEventStream } from "../hooks/useEventStream";
import { sendCommand, sendMessage } from "../api/loopApi";
import { colors, fonts } from "../theme";

interface ChatViewProps {
  channelId: string | null;
  initialRunningBot?: boolean;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
}

export function ChatView({ channelId, initialRunningBot, scrollToMessageId, onScrollComplete }: ChatViewProps) {
  const { messages, loading, loadMore, hasMore, addMessage } =
    useMessages(channelId, scrollToMessageId);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);
  const [streamingContent, setStreamingContent] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(initialRunningBot ?? false);
  const [highlightedMsgId, setHighlightedMsgId] = useState<number | null>(null);
  const [toolActivity, setToolActivity] = useState<{ tool_name: string; input: string } | null>(null);
  const [agentActivity, setAgentActivity] = useState<AgentActivityData | null>(null);
  const [completionInfo, setCompletionInfo] = useState<{ duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null>(null);

  const handleEvent = useCallback(
    (event: WSEvent) => {
      if (event.type === "message.streaming") {
        const data = event.data as MessageStreamingData;
        setStreamingContent(data.content);
        return;
      }
      if (event.type === "message.created") {
        const data = event.data as MessageCreatedData;
        if (data.is_bot) {
          setStreamingContent(null);
        }
        addMessage({
          id: event.timestamp,
          channel_id: event.channel_id,
          msg_id: data.msg_id,
          author_id: data.author_id,
          author_name: data.author_name,
          content: data.content,
          is_bot: data.is_bot,
          created_at: new Date(event.timestamp).toISOString(),
        });
        return;
      }
      if (event.type === "tool.use") {
        const data = event.data as ToolUseData;
        setToolActivity({ tool_name: data.tool_name, input: data.input });
        return;
      }
      if (event.type === "agent.activity") {
        const data = event.data as AgentActivityData;
        setAgentActivity(data);
        return;
      }
      if (event.type === "agent.status") {
        const data = event.data as AgentStatusData;
        if (data.status === "running") {
          setIsRunning(true);
          setCompletionInfo(null);
        } else {
          setIsRunning(false);
          setToolActivity(null);
          setAgentActivity(null);
          if (data.status === "completed" && (data.duration_ms || data.stop_reason)) {
            setCompletionInfo({
              duration_ms: data.duration_ms,
              num_turns: data.num_turns,
              stop_reason: data.stop_reason,
              model: data.model,
            });
          }
        }
        return;
      }
    },
    [addMessage],
  );

  useEventStream({ channelId, onEvent: handleEvent });

  // Auto-scroll to bottom on new messages or streaming updates.
  useEffect(() => {
    if (autoScrollRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages, streamingContent, toolActivity, agentActivity]);

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

  // Find the last user message ID for the 👀 indicator.
  const lastUserMsgId = (() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m && !m.is_bot) return m.msg_id;
    }
    return null;
  })();

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
      <div style={styles.container}>
        <div style={styles.welcome}>
          <WelcomeScreen />
        </div>
        <div style={styles.inputBar}>
          <ChatInput channelId={channelId} onSent={scrollToBottom} />
        </div>
      </div>
    );
  }

  return (
    <div style={styles.container}>
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
              showEyes={isRunning && !msg.is_bot && msg.msg_id === lastUserMsgId}
              highlighted={msg.id === highlightedMsgId}
            />
          ))}
          {isRunning && agentActivity && (
            <AgentActivityIndicator activity={agentActivity} />
          )}
          {toolActivity && !streamingContent && isRunning && (
            <ToolActivityIndicator toolName={toolActivity.tool_name} input={toolActivity.input} />
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
        <ChatInput channelId={channelId} isRunning={isRunning} onSent={scrollToBottom} />
      </div>
      <div style={styles.isolationLabel}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={isRunning ? "#48bb78" : colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
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
    <div style={styles.welcomeContent}>
      <img src="./loop.png" alt="Loop" style={{ width: 128, height: 128, opacity: 0.7 }} />
      <div style={styles.welcomeTitle}>What can we build?</div>
    </div>
  );
}

function MessageBubble({ message, showEyes, highlighted }: { message: Message; showEyes?: boolean; highlighted?: boolean }) {
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
          backgroundColor: isUser ? "#2f2f2f" : "transparent",
          borderRadius: isUser ? "18px 18px 4px 18px" : "18px 18px 18px 4px",
          maxWidth: "85%",
          padding: isUser ? "10px 16px" : "4px 0",
        }}
      >
        {!isUser && (
          <div style={styles.header}>
            <span style={{ ...styles.author, color: colors.active }}>
              {message.author_name || message.author_id}
            </span>
            <span style={styles.time}>{time}</span>
          </div>
        )}
        <div style={styles.content}>
          <MarkdownContent content={message.content} />
        </div>
        {isUser && (
          <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 6, marginTop: 4 }}>
            {showEyes && <span style={{ fontSize: 14 }} title="Processing...">&#128064;</span>}
            <span style={styles.time}>{time}</span>
          </div>
        )}
      </div>
    </div>
  );
}

function StreamingBubble({ content }: { content: string }) {
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
          <span style={{ ...styles.author, color: colors.active }}>
            agent
          </span>
          <span style={{ ...styles.time, fontStyle: "italic" }}>streaming...</span>
        </div>
        <div style={styles.content}>
          <MarkdownContent content={content} />
        </div>
      </div>
    </div>
  );
}

function AgentActivityIndicator({ activity }: { activity: AgentActivityData }) {
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
      <span style={{ opacity: 0.5 }}>&#9203;</span>
      <span style={{ color: colors.textDim }}>{parts.join(" · ")}</span>
    </div>
  );
}

function ToolActivityIndicator({ toolName, input }: { toolName: string; input: string }) {
  const summary = input.length > 80 ? input.slice(0, 80) + "..." : input;
  return (
    <div style={activityStyle}>
      <span style={{ opacity: 0.5 }}>&#9881;</span>
      <span style={{ color: colors.textMuted, fontWeight: 500 }}>{toolName}</span>
      {summary && <span style={{ opacity: 0.7 }}>{summary}</span>}
    </div>
  );
}

const activityStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  marginBottom: 8,
  padding: "4px 0",
  fontSize: 12,
  color: colors.textDim,
  fontFamily: fonts.mono,
};

function MarkdownContent({ content }: { content: string }) {
  const parts = parseMarkdown(content);
  return <>{parts}</>;
}

function parseMarkdown(text: string): React.ReactNode[] {
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
        <pre key={nodes.length} style={styles.codeBlock}>
          {lang && <div style={styles.codeLang}>{lang}</div>}
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
        <p key={nodes.length} style={styles.paragraph}>
          {formatInline(line)}
        </p>,
      );
    }
    i++;
  }

  return nodes;
}

function formatInline(text: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline code, bold, italic.
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*)/g;
  let lastIndex = 0;

  for (;;) {
    const match = regex.exec(text);
    if (!match) break;

    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }

    const token = match[0];
    if (token.startsWith("`")) {
      nodes.push(
        <code key={nodes.length} style={styles.inlineCode}>
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      nodes.push(
        <strong key={nodes.length}>{token.slice(2, -2)}</strong>,
      );
    } else if (token.startsWith("*")) {
      nodes.push(<em key={nodes.length}>{token.slice(1, -1)}</em>);
    }

    lastIndex = match.index + token.length;
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
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

function ChatInput({ channelId, isRunning, onSent }: { channelId: string; isRunning?: boolean; onSent?: () => void }) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [showMention, setShowMention] = useState(false);
  const [mentionIdx, setMentionIdx] = useState(-1);
  const [showCommands, setShowCommands] = useState(false);
  const [filteredCommands, setFilteredCommands] = useState<CommandDef[]>([]);
  const [cmdSelectedIdx, setCmdSelectedIdx] = useState(0);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const cmdDropdownRef = useRef<HTMLDivElement>(null);

  // Auto-focus textarea on mount.
  useEffect(() => {
    inputRef.current?.focus();
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
        await sendMessage(channelId, trimmed);
      }
      setText("");
      onSent?.();
    } finally {
      setSending(false);
      // Re-focus after React re-enables the textarea on the next render.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [channelId, text, sending, isLoopCommand]);

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
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSend();
      }
    },
    [handleSend, showMention, acceptMention, showCommands, filteredCommands, cmdSelectedIdx, acceptCommand],
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
      {isRunning ? (
        <button
          style={{ ...styles.sendButton, background: "#c53030" }}
          onClick={handleStop}
          title="Stop"
        >
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
            <rect x="2" y="2" width="10" height="10" rx="2" fill="currentColor"/>
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
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M8 14V2M8 2L3 7M8 2L13 7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        </button>
      )}
    </div>
  );
}

const commandStyles: Record<string, React.CSSProperties> = {
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
    boxShadow: "0 4px 12px rgba(0,0,0,0.4)",
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
    color: colors.active,
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

const mentionStyles: Record<string, React.CSSProperties> = {
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
    boxShadow: "0 4px 12px rgba(0,0,0,0.4)",
  },
  item: {
    padding: "8px 12px",
    borderRadius: 6,
    cursor: "pointer",
    backgroundColor: colors.selectedBg,
  },
  name: {
    color: colors.active,
    fontWeight: 600,
    fontSize: 13,
    fontFamily: fonts.sans,
  },
};

const styles: Record<string, React.CSSProperties> = {
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
    alignItems: "baseline",
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
    width: 32,
    height: 32,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    background: colors.active,
    border: "none",
    borderRadius: 8,
    color: "#fff",
    cursor: "pointer",
    flexShrink: 0,
  },
};
