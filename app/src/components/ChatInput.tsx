import { useCallback, useEffect, useRef, useState } from "react";
import type { Message } from "../types";
import { sendCommand, sendMessage } from "../api/loopApi";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";

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

function buildInputStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
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

export interface ChatInputProps {
  channelId: string;
  messages: Message[];
  isRunning?: boolean;
  mode: "agent" | "plan";
  setMode: (m: "agent" | "plan") => void;
  onDismissCards?: () => void;
  onSent?: () => void;
}

export function ChatInput({ channelId, messages, isRunning, mode, setMode, onDismissCards, onSent }: ChatInputProps) {
  const { colors } = useTheme();
  const styles = buildInputStyles(colors);
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
