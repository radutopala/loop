import { useCallback, useEffect, useRef } from "react";
import type { Message, MessageCreatedData, WSEvent } from "../types";
import { useMessages } from "../hooks/useMessages";
import { useEventStream } from "../hooks/useEventStream";
import { colors, fonts } from "../theme";

interface ChatViewProps {
  channelId: string | null;
}

export function ChatView({ channelId }: ChatViewProps) {
  const { messages, loading, loadMore, hasMore, addMessage } =
    useMessages(channelId);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const autoScrollRef = useRef(true);

  const handleEvent = useCallback(
    (event: WSEvent) => {
      if (event.type !== "message.created") return;
      const data = event.data as MessageCreatedData;
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
    },
    [addMessage],
  );

  useEventStream({ channelId, onEvent: handleEvent });

  // Auto-scroll to bottom on new messages.
  useEffect(() => {
    if (autoScrollRef.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages]);

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

  if (!channelId) {
    return (
      <div style={styles.empty}>
        <span style={{ color: colors.textMuted }}>
          Select a channel to view messages
        </span>
      </div>
    );
  }

  return (
    <div style={styles.container}>
      <div ref={containerRef} style={styles.messages} onScroll={handleScroll}>
        {loading && messages.length === 0 && (
          <div style={styles.loading}>Loading messages...</div>
        )}
        {hasMore && (
          <button onClick={loadMore} style={styles.loadMore}>
            {loading ? "Loading..." : "Load older messages"}
          </button>
        )}
        {messages.map((msg) => (
          <MessageBubble key={msg.msg_id} message={msg} />
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: Message }) {
  const time = new Date(message.created_at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });

  return (
    <div style={styles.bubble}>
      <div style={styles.header}>
        <span
          style={{
            ...styles.author,
            color: message.is_bot ? colors.active : colors.textLight,
          }}
        >
          {message.author_name || message.author_id}
        </span>
        <span style={styles.time}>{time}</span>
      </div>
      <div style={styles.content}>
        <MarkdownContent content={message.content} />
      </div>
    </div>
  );
}

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

const styles: Record<string, React.CSSProperties> = {
  container: {
    display: "flex",
    flexDirection: "column",
    flex: 1,
    overflow: "hidden",
  },
  empty: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    flex: 1,
  },
  messages: {
    flex: 1,
    overflowY: "auto",
    padding: "12px 16px",
  },
  loading: {
    textAlign: "center",
    color: colors.textMuted,
    padding: 20,
  },
  loadMore: {
    display: "block",
    margin: "0 auto 12px",
    padding: "4px 12px",
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.textMuted,
    cursor: "pointer",
    fontFamily: fonts.sans,
    fontSize: 12,
  },
  bubble: {
    marginBottom: 12,
  },
  header: {
    display: "flex",
    alignItems: "baseline",
    gap: 8,
    marginBottom: 2,
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
    lineHeight: 1.5,
    color: colors.text,
    wordBreak: "break-word" as const,
  },
  paragraph: {
    margin: "2px 0",
  },
  codeBlock: {
    backgroundColor: colors.surface,
    borderRadius: 6,
    padding: "8px 12px",
    margin: "6px 0",
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
};
