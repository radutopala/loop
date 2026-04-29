import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatState } from "../../hooks/useChatState";
import type { RootEntry } from "../../api/files";
import type { Message } from "../../types";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { LoopLogo } from "../shared/LoopLogo";
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
  const { messages, loading, isRunning } = chatState;
  const dismissCards = useCallback(() => { chatState.clearAskUser(); chatState.clearExitPlan(); }, [chatState]);
  const messagesRef = useRef<ChatMessagesHandle>(null);
  const [quotedMessage, setQuotedMessage] = useState<Message | null>(null);
  const clearQuote = useCallback(() => setQuotedMessage(null), []);

  const scrollToBottom = useCallback(() => {
    messagesRef.current?.scrollToBottom();
  }, []);

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
          <ChatInput channelId={channelId} messages={messages} roots={roots} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} quotedMessage={quotedMessage} onClearQuote={clearQuote} />
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
        <ChatInput channelId={channelId} messages={messages} roots={roots} isRunning={isRunning} mode={chatState.mode} setMode={chatState.setMode} onDismissCards={dismissCards} onSent={scrollToBottom} quotedMessage={quotedMessage} onClearQuote={clearQuote} />
      </div>
      <div style={styles.isolationLabel}>
        <LoopInfinityIcon color={isRunning ? undefined : colors.textDim} animated={isRunning} isDark={colors.isDark} />
        Running non-interactively in an isolated Docker container
      </div>
    </div>
  );
}

function LoopInfinityIcon({ color, animated, isDark = true }: { color?: string; animated?: boolean; isDark?: boolean }) {
  const pathRef = useRef<SVGPathElement>(null);
  const trailRef = useRef<SVGPathElement>(null);
  const headRef = useRef<SVGCircleElement>(null);
  const gradRef = useRef<SVGLinearGradientElement>(null);
  const rafRef = useRef(0);

  useEffect(() => {
    if (!animated || !pathRef.current || !trailRef.current || !headRef.current || !gradRef.current) return;
    const path = pathRef.current;
    const trail = trailRef.current;
    const headEl = headRef.current;
    const grad = gradRef.current;
    const totalLen = path.getTotalLength();
    const trailFrac = 0.35;
    const speed = 0.25;
    trail.style.strokeDasharray = `${totalLen * trailFrac} ${totalLen * (1 - trailFrac)}`;

    const COLORS = isDark
      ? [[34, 197, 94], [134, 239, 172], [245, 158, 11], [251, 191, 36], [34, 197, 94]]
      : [[60, 60, 60], [120, 120, 120], [80, 80, 80], [140, 140, 140], [60, 60, 60]];
    function colorAt(p: number) {
      const idx = p * (COLORS.length - 1);
      const lo = Math.floor(idx);
      const hi = Math.min(lo + 1, COLORS.length - 1);
      const t = idx - lo;
      return COLORS[lo]!.map((v, i) => Math.round(v + (COLORS[hi]![i]! - v) * t));
    }
    function rgb(c: number[]) { return `rgb(${c[0]},${c[1]},${c[2]})`; }

    let progress = 0;
    let prev = 0;
    const stops = grad.querySelectorAll("stop");
    function frame(t: number) {
      const dt = prev ? (t - prev) / 1000 : 0;
      prev = t;
      progress = (progress + speed * dt) % 1;
      const shift = (t * 0.0001) % 1;
      stops.forEach((s, i) => {
        s.setAttribute("stop-color", rgb(colorAt(((i / (stops.length - 1)) + shift) % 1)));
      });
      trail.style.strokeDashoffset = `${totalLen * (1 - progress)}`;
      const pulse = 0.7 + 0.3 * Math.sin(t * 0.003);
      trail.setAttribute("opacity", String(pulse));
      // Move the head circle along the path at the trail's leading edge.
      const pt = path.getPointAtLength(progress * totalLen);
      headEl.setAttribute("cx", String(pt.x));
      headEl.setAttribute("cy", String(pt.y));
      const headColor = colorAt((progress + shift) % 1);
      headEl.setAttribute("fill", rgb(headColor));
      headEl.setAttribute("opacity", String(Math.min(1, pulse + 0.2)));
      rafRef.current = requestAnimationFrame(frame);
    }
    rafRef.current = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(rafRef.current);
  }, [animated, isDark]);

  const d = "M0 0c-43-57.3-86-86-128.7-86a86 86 0 1 0 0 172c42.7 0 85.7-28.7 128.7-86Zm0 0c43 57.3 86 86 128.7 86a86 86 0 0 0 0-172c-42.7 0-85.7 28.7-128.7 86Z";
  const id = "loop-inf-grad";
  return (
    <svg width="16" height="16" viewBox="-230 -100 460 200">
      {animated ? (
        <defs>
          <linearGradient ref={gradRef} id={id} gradientUnits="userSpaceOnUse" x1="-215" y1="0" x2="215" y2="0">
            <stop offset="0%" stopColor={isDark ? "#22c55e" : "#3c3c3c"} />
            <stop offset="30%" stopColor={isDark ? "#86efac" : "#787878"} />
            <stop offset="50%" stopColor={isDark ? "#f59e0b" : "#505050"} />
            <stop offset="70%" stopColor={isDark ? "#fbbf24" : "#8c8c8c"} />
            <stop offset="100%" stopColor={isDark ? "#22c55e" : "#3c3c3c"} />
          </linearGradient>
        </defs>
      ) : null}
      <path ref={pathRef} fill="none" stroke="none" d={d} />
      {animated ? (
        <>
          <path fill="none" stroke={isDark ? "rgba(34,197,94,0.15)" : "rgba(0,0,0,0.1)"} strokeWidth="28" strokeLinecap="round" strokeLinejoin="round" d={d} />
          <path ref={trailRef} fill="none" stroke={`url(#${id})`} strokeWidth="28" strokeLinecap="round" strokeLinejoin="round" d={d} />
          <circle ref={headRef} r="16" fill={isDark ? "#22c55e" : "#3c3c3c"} />
        </>
      ) : (
        <path fill="none" stroke={color || "currentColor"} strokeWidth="28" strokeLinecap="round" strokeLinejoin="round" d={d} />
      )}
    </svg>
  );
}

function WelcomeScreen() {
  return (
    <div style={{ display: "flex", flexDirection: "column" as const, alignItems: "center", gap: 16 }}>
      <LoopLogo />
    </div>
  );
}
