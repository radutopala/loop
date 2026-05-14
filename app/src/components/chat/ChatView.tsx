import { useCallback, useRef, useState } from "react";
import type { ChatState } from "../../hooks/useChatState";
import type { RootEntry } from "../../api/files";
import type { Message } from "../../types";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { LoopLogo } from "../shared/LoopLogo";
import { LoopInfinityIcon } from "../LoopInfinityIcon";
import { ChatInput } from "./ChatInput";
import { ChatMessages } from "./ChatMessages";
import type { ChatMessagesHandle } from "./ChatMessages";

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
  const dismissCards = useCallback(() => { chatState.clearAskUser(); chatState.clearExitPlan(); chatState.clearGateApproval("chat"); }, [chatState]);
  const messagesRef = useRef<ChatMessagesHandle>(null);
  const [quotedMessage, setQuotedMessage] = useState<Message | null>(null);
  const clearQuote = useCallback(() => setQuotedMessage(null), []);

  const scrollToBottom = useCallback(() => {
    messagesRef.current?.scrollToBottom();
  }, []);

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
          <ChatInput channelId={channelId} messages={messages} roots={roots} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} quotedMessage={quotedMessage} onClearQuote={clearQuote} pendingGateReqId={chatState.gateApprovals["chat"]?.req_id ?? null} />
        </div>

        <div style={styles.isolationLabel}>
          <LoopInfinityIcon color={colors.textDim} isDark={colors.isDark} />
          Running non-interactively in an isolated Docker container
        </div>
      </div>
    );
  }

  return (
    <div style={{ ...styles.container, zoom: fontSizes.chat / 13 }}>
      <ChatMessages ref={messagesRef} channelId={channelId} chatState={chatState} scrollToMessageId={scrollToMessageId} onScrollComplete={onScrollComplete} onQuote={setQuotedMessage} />
      <div style={styles.inputBar}>
        <ChatInput channelId={channelId} messages={messages} roots={roots} isRunning={isRunning} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} quotedMessage={quotedMessage} onClearQuote={clearQuote} pendingGateReqId={chatState.gateApprovals["chat"]?.req_id ?? null} />
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
