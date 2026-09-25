import { useCallback, useEffect, useRef, useState } from "react";
import type { RootEntry } from "../../api/files";
import type { ChatState } from "../../hooks/useChatState";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { Message } from "../../types";
import { LoopInfinityIcon } from "../LoopInfinityIcon";
import { LoopLogo } from "../shared/LoopLogo";
import { ChatFindBar } from "./ChatFindBar";
import { ChatInput } from "./ChatInput";
import type { ChatMessagesHandle } from "./ChatMessages";
import { ChatMessages } from "./ChatMessages";

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
    messagesArea: {
      display: "flex",
      flexDirection: "column",
      flex: 1,
      minHeight: 0,
      position: "relative",
    },
    // Top right, clear of the scrollbar.
    findToggle: {
      position: "absolute",
      top: 8,
      right: 16,
      zIndex: 3,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      width: 24,
      height: 24,
      padding: 0,
      border: `1px solid ${colors.border}`,
      borderRadius: 4,
      background: colors.bg,
      color: colors.textMuted,
      cursor: "pointer",
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
      // Wrap the icon + label as a group and center the text so the footer
      // stays centered (not left-aligned) when the chat pane is too narrow
      // for it to fit on one line.
      flexWrap: "wrap",
      textAlign: "center",
      gap: 6,
      padding: "0 12px 12px",
      fontSize: 11,
      color: colors.textDim,
      fontFamily: fonts.mono,
    },
  };
}

interface ChatViewProps {
  channelId: string | null;
  chatState: ChatState;
  roots?: RootEntry[];
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
}

export function ChatView({ channelId, chatState, roots, scrollToMessageId, onScrollComplete }: ChatViewProps) {
  const { colors, fontSizes } = useTheme();
  const styles = buildStyles(colors);
  const { items, liveTail, messages, loading, isRunning } = chatState;
  // Only the chat-sourced gate card is dismissed locally. Ask/plan cards stay
  // up until the backend says the park is resolved (agent.ask_resolved /
  // agent.plan_resolved) — hiding them early leaves the channel blocked with
  // nothing left to answer.
  const dismissGate = useCallback(() => {
    chatState.clearGateApproval("chat");
  }, [chatState]);
  const messagesRef = useRef<ChatMessagesHandle>(null);
  const [quotedMessage, setQuotedMessage] = useState<Message | null>(null);
  const clearQuote = useCallback(() => setQuotedMessage(null), []);

  const scrollToBottom = useCallback(() => {
    messagesRef.current?.scrollToBottom();
  }, []);

  // Find in chat. A match is shown through the same scroll-to-message path a
  // message link takes; findTarget is cleared once it's in view, so new
  // messages arriving afterwards don't pull the view back to it.
  const [findOpen, setFindOpen] = useState(false);
  const [findFocusKey, setFindFocusKey] = useState(0);
  const [findTarget, setFindTarget] = useState<number | null>(null);
  const [findTerm, setFindTerm] = useState("");
  const jumpToMatch = useCallback((messageId: number, term: string) => {
    setFindTarget(messageId);
    setFindTerm(term);
  }, []);
  const openFind = useCallback(() => {
    setFindOpen(true);
    setFindFocusKey((k) => k + 1);
  }, []);
  const closeFind = useCallback(() => {
    setFindOpen(false);
    setFindTarget(null);
    setFindTerm("");
  }, []);
  const handleScrollComplete = useCallback(() => {
    if (findTarget !== null) setFindTarget(null);
    else onScrollComplete?.();
  }, [findTarget, onScrollComplete]);
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "f") {
      e.preventDefault();
      openFind();
    }
  };

  useEffect(() => {
    closeFind();
  }, [channelId, closeFind]);

  const isEmpty = items.length === 0 && liveTail.length === 0 && !loading;

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
          <ChatInput
            channelId={channelId}
            messages={messages}
            roots={roots}
            mode={chatState.mode}
            setMode={chatState.setMode}
            onDismissGate={dismissGate}
            onSent={scrollToBottom}
            quotedMessage={quotedMessage}
            onClearQuote={clearQuote}
            pendingGateReqId={chatState.gateApprovals["chat"]?.req_id ?? null}
            hasPendingExitPlan={!!chatState.exitPlanRequest}
            hasPendingAskUser={!!chatState.askUserQuestions}
          />
        </div>

        <div style={styles.isolationLabel}>
          <LoopInfinityIcon color={colors.textDim} isDark={colors.isDark} />
          Running non-interactively in an isolated Docker container
        </div>
      </div>
    );
  }

  return (
    <div style={{ ...styles.container, zoom: fontSizes.chat / 13 }} onKeyDown={handleKeyDown}>
      {findOpen && <ChatFindBar channelId={channelId} focusKey={findFocusKey} onJump={jumpToMatch} onClose={closeFind} />}
      <div style={styles.messagesArea}>
        <ChatMessages
          ref={messagesRef}
          channelId={channelId}
          chatState={chatState}
          scrollToMessageId={findTarget ?? scrollToMessageId}
          findTerm={findOpen ? findTerm : undefined}
          onScrollComplete={handleScrollComplete}
          onQuote={setQuotedMessage}
        />
        {!findOpen && (
          <button
            onClick={openFind}
            title={`Find in chat (${navigator.platform.includes("Mac") ? "\u2318F" : "Ctrl+F"})`}
            data-testid="chat-find-toggle"
            style={styles.findToggle}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = colors.textLight;
              e.currentTarget.style.borderColor = colors.textDim;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = colors.textMuted;
              e.currentTarget.style.borderColor = colors.border;
            }}
          >
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="7" />
              <path d="M20 20L16 16" />
            </svg>
          </button>
        )}
      </div>
      <div style={styles.inputBar}>
        <ChatInput
          channelId={channelId}
          messages={messages}
          roots={roots}
          isRunning={isRunning}
          mode={chatState.mode}
          setMode={chatState.setMode}
          onDismissGate={dismissGate}
          onSent={scrollToBottom}
          quotedMessage={quotedMessage}
          onClearQuote={clearQuote}
          pendingGateReqId={chatState.gateApprovals["chat"]?.req_id ?? null}
          hasPendingExitPlan={!!chatState.exitPlanRequest}
          hasPendingAskUser={!!chatState.askUserQuestions}
        />
      </div>
      <div style={styles.isolationLabel}>
        <LoopInfinityIcon color={isRunning ? undefined : colors.textDim} animated={isRunning} isDark={colors.isDark} />
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
