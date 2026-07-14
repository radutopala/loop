import { useCallback, useEffect, useRef, useState } from "react";
import type { Message } from "../../types";
import { resolveGateApproval, sendCommand, sendMessage } from "../../api/loopApi";
import { fetchComposerHistory, resolveAsk, resolvePlan } from "../../api/channels";
import { AgentConfigPill } from "./AgentConfigPill";
import { firstClipboardImage, uploadPastedImage } from "../../utils/clipboardImage";
import { fetchShortcuts, type PromptShortcut } from "../../api/configApi";
import { searchFiles, type FileSearchResult, type RootEntry } from "../../api/files";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { storageGetJSON, storageSetJSON } from "../../utils/storage";
// Draft text per channel — persisted to localStorage across app restarts.
const DRAFT_KEY = "loop-chat-drafts";
const draftText = {
  get(channelId: string): string | undefined {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY);
    return drafts?.[channelId];
  },
  set(channelId: string, text: string) {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY) ?? {};
    drafts[channelId] = text;
    storageSetJSON(DRAFT_KEY, drafts);
  },
  delete(channelId: string) {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY) ?? {};
    delete drafts[channelId];
    storageSetJSON(DRAFT_KEY, drafts);
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
  { name: "workflows", description: "List available workflows" },
  { name: "workflow-run", description: "Run a workflow", usage: "<name>" },
  { name: "workflow-runs", description: "List recent workflow runs" },
  { name: "workflow-cancel", description: "Cancel a workflow run", usage: "<run_id>" },
  { name: "workflow-retry", description: "Retry a workflow run", usage: "<run_id>" },
  { name: "workflow-delete", description: "Delete a workflow run", usage: "<run_id>" },
];

export type SendMode = "queue" | "interrupt";
const SEND_MODE_KEY = "loop-send-mode";

export interface ChatInputProps {
  channelId: string;
  messages: Message[];
  roots?: RootEntry[];
  isRunning?: boolean;
  mode: "agent" | "plan";
  setMode: (m: "agent" | "plan") => void;
  onDismissCards?: () => void;
  onSent?: () => void;
  quotedMessage?: Message | null;
  onClearQuote?: () => void;
  // When a gate approval popup is showing, sending a message auto-denies the
  // gate, interrupts the resumed run, and queues the user's text — same flow
  // as ApprovalCard's "Deny with prompt".
  pendingGateReqId?: string | null;
  // When an ExitPlanMode card is parked on this channel, sending a free-text
  // message auto-denies the plan with the user's text as the deny prompt —
  // same flow as ExitPlanCard's "Request changes". Without this, the FE
  // dismisses the card locally but the backend stays parked forever and the
  // drain loop never picks up the user's queued messages.
  hasPendingExitPlan?: boolean;
  // When an AskUserQuestion card is parked on this channel, sending a free-text
  // message routes through `resolveAsk(answer)` so the backend clears the
  // park flag and inserts the user's text as a priority-bumped continuation.
  // Without this, the channel stays parked and queued messages never drain.
  hasPendingAskUser?: boolean;
}

function buildQuotePrefix(msg: Message): string {
  return msg.content.split("\n").map(l => `> ${l}`).join("\n") + "\n\n";
}

export function ChatInput({ channelId, messages, roots, isRunning, mode, setMode, onDismissCards, onSent, quotedMessage, onClearQuote, pendingGateReqId, hasPendingExitPlan, hasPendingAskUser }: ChatInputProps) {
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
  const [shortcuts, setShortcuts] = useState<PromptShortcut[]>([]);
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [filteredShortcuts, setFilteredShortcuts] = useState<PromptShortcut[]>([]);
  const [shortcutSelectedIdx, setShortcutSelectedIdx] = useState(0);
  const [sendMode, setSendMode] = useState<SendMode>(() => (storageGetJSON<string>(SEND_MODE_KEY) as SendMode) || "queue");
  const [showSendMenu, setShowSendMenu] = useState(false);
  // Optimistic stop: flip to true on stop press, so the UI updates instantly
  // without waiting for the backend round-trip. Reset when isRunning prop changes.
  const [stoppedOptimistic, setStoppedOptimistic] = useState(false);
  const prevIsRunning = useRef(isRunning);
  if (prevIsRunning.current !== isRunning) {
    prevIsRunning.current = isRunning;
    if (stoppedOptimistic) setStoppedOptimistic(false);
  }
  const effectiveIsRunning = isRunning && !stoppedOptimistic;
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const cmdDropdownRef = useRef<HTMLDivElement>(null);
  const shortcutDropdownRef = useRef<HTMLDivElement>(null);

  // File picker (`@<partial>`).
  const [showFilePicker, setShowFilePicker] = useState(false);
  const [filePickerResults, setFilePickerResults] = useState<FileSearchResult[]>([]);
  const [filePickerIdx, setFilePickerIdx] = useState(0);
  const [fileAtIdx, setFileAtIdx] = useState(-1);
  const fileSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const fileSearchSeqRef = useRef(0);
  const filePickerDropdownRef = useRef<HTMLDivElement>(null);

  // ── Message history (ArrowUp / ArrowDown) ──
  // Stores user-sent message contents for this channel; -1 = composing new text.
  const historyRef = useRef<string[]>([]);
  const historyIdxRef = useRef(-1);
  const draftRef = useRef("");
  const historyChannelRef = useRef<string | null>(null);

  // Keep history scoped to the active channel and clear stale entries on
  // switch. The list comes from a dedicated endpoint independent of timeline
  // pagination — sourcing it from the loaded `messages` page capped ArrowUp
  // recall at whatever the first page happened to contain. Until the fetch
  // resolves (or if it fails), fall back to the loaded page so history keeps
  // working offline.
  useEffect(() => {
    const channelChanged = historyChannelRef.current !== channelId;
    historyChannelRef.current = channelId;

    if (channelChanged) {
      historyIdxRef.current = -1;
      draftRef.current = draftText.get(channelId) ?? "";
      historyRef.current = messages.filter((m) => !m.is_bot).map((m) => m.content);
      // No cancellation flag on purpose: StrictMode / remounts run the
      // cleanup right after the first effect pass, and the second pass sees
      // historyChannelRef already set so it would never re-fetch — a
      // cancelled-flag guard left history permanently on the page fallback.
      // The channel-match check below is the correct staleness guard: a late
      // response for a channel we've switched away from is discarded.
      fetchComposerHistory(channelId).then((hist) => {
        if (historyChannelRef.current === channelId && hist.length > 0) {
          historyRef.current = hist;
        }
      }).catch(() => { /* keep the page-derived fallback */ });
    }
  }, [channelId]); // eslint-disable-line react-hooks/exhaustive-deps

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

  // Fetch prompt shortcuts when channel changes.
  useEffect(() => {
    fetchShortcuts(channelId).then(setShortcuts).catch(() => setShortcuts([]));
  }, [channelId]);

  // Scroll selected shortcut item into view.
  useEffect(() => {
    const container = shortcutDropdownRef.current;
    if (!container || !showShortcuts) return;
    const item = container.children[shortcutSelectedIdx] as HTMLElement | undefined;
    item?.scrollIntoView({ block: "nearest" });
  }, [shortcutSelectedIdx, showShortcuts]);

  // Scroll selected file picker item into view.
  useEffect(() => {
    const container = filePickerDropdownRef.current;
    if (!container || !showFilePicker) return;
    const item = container.children[filePickerIdx] as HTMLElement | undefined;
    item?.scrollIntoView({ block: "nearest" });
  }, [filePickerIdx, showFilePicker]);

  // Cancel any pending file-search debounce on unmount.
  useEffect(() => {
    return () => {
      if (fileSearchTimerRef.current) clearTimeout(fileSearchTimerRef.current);
    };
  }, []);

  // Focus textarea when a quote is set.
  useEffect(() => {
    if (quotedMessage) inputRef.current?.focus();
  }, [quotedMessage]);

  const changeSendMode = useCallback((m: SendMode) => {
    setSendMode(m);
    storageSetJSON(SEND_MODE_KEY, m);
    setShowSendMenu(false);
  }, []);

  const isLoopCommand = useCallback((t: string) => t.trimStart().startsWith("/loop"), []);

  const handlePaste = useCallback(async (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    // Find the first image item; if none, let the default text-paste happen.
    const file = firstClipboardImage(e.clipboardData);
    if (!file) return;
    e.preventDefault();

    try {
      const path = await uploadPastedImage(channelId, file);
      const ta = inputRef.current;
      if (!ta) {
        setText((prev) => prev + path);
        return;
      }
      const start = ta.selectionStart ?? text.length;
      const end = ta.selectionEnd ?? text.length;
      const next = text.slice(0, start) + path + text.slice(end);
      setText(next);
      draftText.set(channelId, next);
      // Restore the caret just after the inserted path.
      requestAnimationFrame(() => {
        if (!inputRef.current) return;
        const pos = start + path.length;
        inputRef.current.selectionStart = pos;
        inputRef.current.selectionEnd = pos;
        inputRef.current.focus();
      });
    } catch (err) {
      console.error("[paste-image] upload failed", err);
    }
  }, [channelId, text]);

  const handleStop = useCallback(async () => {
    setStoppedOptimistic(true);
    onDismissCards?.();
    await sendCommand(channelId, "stop");
  }, [channelId, onDismissCards]);

  const handleSend = useCallback(async (overrideMode?: SendMode) => {
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
        const content = quotedMessage ? buildQuotePrefix(quotedMessage) + trimmed : trimmed;
        // When the channel is parked on an AskUserQuestion card, route the
        // send through `resolveAsk(answer)` so the backend clears the park
        // flag and inserts the user's text as a priority-bumped continuation.
        // `resolveAsk` already inserts the message, so skip the regular
        // sendMessage call here.
        if (hasPendingAskUser) {
          await resolveAsk(channelId, "answer", content, mode);
        } else if (hasPendingExitPlan) {
          // When the channel is parked on an ExitPlanMode card, route the send
          // through `resolvePlan(deny, prompt)` so the backend clears the park
          // flag, inserts the user's text as a priority-bumped continuation,
          // and resumes the drain. `resolvePlan` already inserts the message,
          // so skip the regular sendMessage call here.
          await resolvePlan(channelId, "deny", content, mode);
        } else if (pendingGateReqId) {
          // When a gate approval popup is pending, auto-deny it and force
          // interrupt — same shape as ApprovalCard's "Deny with prompt".
          await resolveGateApproval(pendingGateReqId, "deny");
          await sendMessage(channelId, content, mode, true);
        } else {
          const effectiveMode = overrideMode ?? sendMode;
          const interrupt = effectiveIsRunning && effectiveMode === "interrupt";
          await sendMessage(channelId, content, mode, interrupt || undefined);
        }
      }
      // Push to history.
      historyRef.current.push(trimmed);
      historyIdxRef.current = -1;
      draftRef.current = "";

      setText("");
      draftText.delete(channelId);
      onClearQuote?.();
      onDismissCards?.();
      onSent?.();
    } finally {
      setSending(false);
      // Re-focus after React re-enables the textarea on the next render.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [channelId, text, sending, mode, isLoopCommand, effectiveIsRunning, sendMode, quotedMessage, onClearQuote, onDismissCards, pendingGateReqId, hasPendingExitPlan, hasPendingAskUser]);

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

  const updateShortcutDropdown = useCallback((val: string) => {
    const trimmed = val.trimStart();
    if (!trimmed.startsWith("#") || shortcuts.length === 0) {
      setShowShortcuts(false);
      return;
    }
    const query = trimmed.slice(1).toLowerCase();
    const matches = query
      ? shortcuts.filter((s) => s.name.toLowerCase().startsWith(query))
      : shortcuts;
    setFilteredShortcuts(matches);
    setShortcutSelectedIdx(0);
    setShowShortcuts(matches.length > 0);
  }, [shortcuts]);

  const acceptShortcut = useCallback(async (shortcut: PromptShortcut) => {
    setShowShortcuts(false);
    setText("");
    draftText.delete(channelId);
    setSending(true);
    // Prepend the shortcut's #name so the sent message records which shortcut
    // produced it — the chat bubble and history show the name, not just the
    // expanded prompt body.
    const composed = `#${shortcut.name}\n${shortcut.prompt}`;
    try {
      if (pendingGateReqId) {
        await resolveGateApproval(pendingGateReqId, "deny");
        await sendMessage(channelId, composed, mode, true);
      } else {
        await sendMessage(channelId, composed, mode);
      }
      historyRef.current.push(composed);
      historyIdxRef.current = -1;
      draftRef.current = "";
      onDismissCards?.();
      onSent?.();
    } finally {
      setSending(false);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [channelId, mode, onDismissCards, onSent, pendingGateReqId]);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const val = e.target.value;
      setText(val);
      if (val) draftText.set(channelId, val);
      else draftText.delete(channelId);

      // Check for command autocomplete.
      updateCommandDropdown(val);

      // Check for shortcut autocomplete.
      updateShortcutDropdown(val);

      // Check for @mention / @file autocomplete.
      const pos = e.target.selectionStart;
      const before = val.slice(0, pos);
      const atIdx = before.lastIndexOf("@");
      if (atIdx !== -1 && (atIdx === 0 || before[atIdx - 1] === " " || before[atIdx - 1] === "\n")) {
        const partial = before.slice(atIdx + 1);
        if (!partial.includes(" ") && !partial.includes("\n")) {
          const matchesLoopBot = "LoopBot".toLowerCase().startsWith(partial.toLowerCase());
          if (!roots || roots.length === 0) {
            if (matchesLoopBot) {
              setShowMention(true);
              setMentionIdx(atIdx);
              setShowFilePicker(false);
              return;
            }
          } else {
            setFileAtIdx(atIdx);
            if (fileSearchTimerRef.current) clearTimeout(fileSearchTimerRef.current);
            const seq = ++fileSearchSeqRef.current;
            fileSearchTimerRef.current = setTimeout(async () => {
              const fileResults = await searchFiles(channelId, partial, 30);
              if (seq !== fileSearchSeqRef.current) return;
              const merged: FileSearchResult[] = [];
              if (matchesLoopBot) {
                merged.push({ root_index: -1, rel_path: "LoopBot", name: "@LoopBot mention" });
              }
              merged.push(...fileResults);
              setFilePickerResults(merged);
              setFilePickerIdx(0);
              setShowFilePicker(merged.length > 0);
            }, 120);
            setShowMention(false);
            return;
          }
        }
      }
      setShowMention(false);
      setShowFilePicker(false);
    },
    [updateCommandDropdown, updateShortcutDropdown, channelId, roots],
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

  // Replace `@<partial>` with `@<absolute-path>`. Workspaces can have multiple
  // roots, so a relative path is ambiguous; the agent container mounts each
  // root at its host path, so absolute paths resolve correctly inside it.
  const acceptFile = useCallback((r: FileSearchResult) => {
    if (fileAtIdx < 0) return;
    const root = roots?.find((x) => x.index === r.root_index);
    const absPath = root ? `${root.path.replace(/\/$/, "")}/${r.rel_path}` : r.rel_path;
    const token = r.root_index === -1 ? `@LoopBot ` : `@${absPath} `;
    const pos = inputRef.current?.selectionStart ?? text.length;
    const newText = text.slice(0, fileAtIdx) + token + text.slice(pos);
    setText(newText);
    draftText.set(channelId, newText);
    setShowFilePicker(false);
    requestAnimationFrame(() => {
      const el = inputRef.current;
      if (el) {
        const cursorPos = fileAtIdx + token.length;
        el.focus();
        el.setSelectionRange(cursorPos, cursorPos);
      }
    });
  }, [fileAtIdx, text, channelId, roots]);

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
      // Shortcut picker navigation.
      if (showShortcuts && filteredShortcuts.length > 0) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setShortcutSelectedIdx((i) => Math.min(i + 1, filteredShortcuts.length - 1));
          return;
        }
        if (e.key === "ArrowUp") {
          e.preventDefault();
          setShortcutSelectedIdx((i) => Math.max(i - 1, 0));
          return;
        }
        if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
          e.preventDefault();
          const sc = filteredShortcuts[shortcutSelectedIdx];
          if (sc) acceptShortcut(sc);
          return;
        }
        if (e.key === "Escape") {
          setShowShortcuts(false);
          return;
        }
      }
      // File picker (`@<partial>`) navigation.
      if (showFilePicker && filePickerResults.length > 0) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setFilePickerIdx((i) => Math.min(i + 1, filePickerResults.length - 1));
          return;
        }
        if (e.key === "ArrowUp") {
          e.preventDefault();
          setFilePickerIdx((i) => Math.max(i - 1, 0));
          return;
        }
        if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
          e.preventDefault();
          const r = filePickerResults[filePickerIdx];
          if (r) acceptFile(r);
          return;
        }
        if (e.key === "Escape") {
          setShowFilePicker(false);
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
      if (e.key === "ArrowUp" && !showCommands && !showShortcuts && !showMention && !showFilePicker) {
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
      if (e.key === "ArrowDown" && !showCommands && !showShortcuts && !showMention && !showFilePicker) {
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
        if (e.metaKey || e.ctrlKey) {
          handleSend(sendMode === "interrupt" ? "queue" : "interrupt");
        } else {
          handleSend();
        }
      }
    },
    [handleSend, sendMode, text, channelId, showMention, acceptMention, showCommands, filteredCommands, cmdSelectedIdx, acceptCommand, showShortcuts, filteredShortcuts, shortcutSelectedIdx, acceptShortcut, showFilePicker, filePickerResults, filePickerIdx, acceptFile],
  );

  return (
    <div style={{ position: "relative", ...styles.inputWrapper, flexDirection: "column" }}>
      {quotedMessage && (
        <div style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          width: "100%",
          padding: "4px 0 8px",
          borderBottom: `1px solid ${colors.border}`,
          marginBottom: 8,
          fontSize: 12,
          color: colors.textMuted,
          fontFamily: fonts.sans,
        }}>
          <div style={{ borderLeft: `3px solid ${colors.border}`, paddingLeft: 8, flex: 1, overflow: "hidden", whiteSpace: "nowrap", textOverflow: "ellipsis" }}>
            {quotedMessage.content.length > 120 ? quotedMessage.content.slice(0, 120) + "\u2026" : quotedMessage.content}
          </div>
          <button
            onClick={onClearQuote}
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", fontSize: 14, lineHeight: 1, flexShrink: 0 }}
            title="Remove quote"
          >
            &times;
          </button>
        </div>
      )}
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
      {showShortcuts && filteredShortcuts.length > 0 && (
        <div style={commandStyles.dropdown}>
          <div ref={shortcutDropdownRef} style={commandStyles.scrollArea}>
            {filteredShortcuts.map((sc, i) => (
              <div
                key={sc.name}
                style={{
                  ...commandStyles.item,
                  backgroundColor: i === shortcutSelectedIdx ? colors.selectedBg : "transparent",
                }}
                onMouseDown={(e) => { e.preventDefault(); acceptShortcut(sc); }}
                onMouseEnter={() => setShortcutSelectedIdx(i)}
              >
                <div style={commandStyles.name}>#{sc.name}</div>
                <div style={commandStyles.desc}>{sc.description}</div>
              </div>
            ))}
          </div>
        </div>
      )}
      {showFilePicker && filePickerResults.length > 0 && (
        <div style={commandStyles.dropdown}>
          <div ref={filePickerDropdownRef} style={commandStyles.scrollArea}>
            {filePickerResults.map((r, i) => {
              const rootName = roots?.find((x) => x.index === r.root_index)?.name ?? "";
              const display = r.root_index === -1
                ? `@LoopBot`
                : r.root_index === 0 || !rootName
                  ? `@${r.rel_path}`
                  : `@${rootName}/${r.rel_path}`;
              return (
                <div
                  key={`${r.root_index}:${r.rel_path}`}
                  style={{
                    ...commandStyles.item,
                    backgroundColor: i === filePickerIdx ? colors.selectedBg : "transparent",
                  }}
                  onMouseDown={(e) => { e.preventDefault(); acceptFile(r); }}
                  onMouseEnter={() => setFilePickerIdx(i)}
                >
                  <div style={commandStyles.name}>{display}</div>
                  <div style={commandStyles.desc}>{r.name}</div>
                </div>
              );
            })}
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
        style={{ ...styles.textarea, width: "100%" }}
        value={text}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        placeholder={`Ask Loop anything, / for commands, @ for files${shortcuts.length > 0 ? ", # for shortcuts" : ""} — ${navigator.platform.includes("Mac") ? "⌘" : "Ctrl+"}Enter to ${sendMode === "interrupt" ? "queue" : "interrupt"}`}
        rows={3}
        disabled={sending}
      />
      <div style={{ display: "flex", alignItems: "center", gap: 8, width: "100%" }}>
      {shortcuts.length > 0 && (
        <button
          style={{
            width: 28,
            height: 28,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "transparent",
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            color: colors.textDim,
            cursor: "pointer",
            flexShrink: 0,
            fontFamily: fonts.mono,
            fontSize: 14,
            fontWeight: 600,
          }}
          title="Prompt shortcuts"
          onClick={() => {
            if (showShortcuts) {
              setShowShortcuts(false);
            } else {
              setFilteredShortcuts(shortcuts);
              setShortcutSelectedIdx(0);
              setShowShortcuts(true);
              inputRef.current?.focus();
            }
          }}
        >
          #
        </button>
      )}
      <div style={{ flex: 1 }} />
      <AgentConfigPill channelId={channelId} />
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
      <div style={{ position: "relative", display: "flex", alignItems: "center", flexShrink: 0 }}>
        {effectiveIsRunning ? (
          <button
            style={{ ...styles.sendButton, background: "transparent", border: `1px solid ${colors.textDim}`, color: colors.textDim, borderRadius: "8px 0 0 8px" }}
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
              borderRadius: "8px 0 0 8px",
              opacity: text.trim() && !sending ? 1 : 0.4,
            }}
            onClick={() => handleSend()}
            disabled={!text.trim() || sending}
            title={`${sendMode === "interrupt" ? "Send (interrupt)" : "Send (queue)"} — ${navigator.platform.includes("Mac") ? "⌘" : "Ctrl+"}Enter to ${sendMode === "interrupt" ? "queue" : "interrupt"}`}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M8 14V2M8 2L3 7M8 2L13 7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </button>
        )}
        <button
          style={{
            height: 28,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 2,
            background: effectiveIsRunning ? "transparent" : colors.pillActiveBg,
            border: effectiveIsRunning ? `1px solid ${colors.textDim}` : "none",
            borderLeft: effectiveIsRunning ? "none" : `1px solid ${colors.pillActiveText}33`,
            borderRadius: "0 8px 8px 0",
            color: effectiveIsRunning ? colors.textDim : colors.pillActiveText,
            cursor: "pointer",
            flexShrink: 0,
            padding: "0 6px 0 4px",
            fontFamily: fonts.mono,
            fontSize: 9,
            fontWeight: 600,
            letterSpacing: 0.3,
          }}
          onClick={() => setShowSendMenu(prev => !prev)}
          title={`Send mode — ${navigator.platform.includes("Mac") ? "⌘" : "Ctrl+"}Enter sends with the other mode once`}
        >
          {sendMode === "interrupt" ? "INT" : "Q"}
          <svg width="8" height="8" viewBox="0 0 8 8" fill="none">
            <path d="M1.5 3L4 5.5L6.5 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        </button>
        {showSendMenu && (
          <>
            <div style={{ position: "fixed", inset: 0, zIndex: 999 }} onMouseDown={() => setShowSendMenu(false)} />
            <div style={{
              position: "absolute",
              bottom: "calc(100% + 4px)",
              right: 0,
              backgroundColor: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
              padding: 4,
              minWidth: 140,
              zIndex: 1000,
              boxShadow: `0 4px 12px ${colors.shadow}`,
              fontFamily: fonts.sans,
            }}>
              {([["queue", "Queue", "Messages wait for the agent to finish"], ["interrupt", "Interrupt", "Stop the agent, then send"]] as const).map(([key, label, desc]) => (
                <button
                  key={key}
                  onClick={() => changeSendMode(key)}
                  style={{
                    display: "flex",
                    flexDirection: "column",
                    gap: 1,
                    width: "100%",
                    padding: "5px 8px",
                    border: "none",
                    background: sendMode === key ? colors.selectedBg : "transparent",
                    color: colors.textLight,
                    fontSize: 11,
                    textAlign: "left",
                    cursor: "pointer",
                    borderRadius: 4,
                    fontFamily: fonts.sans,
                  }}
                  onMouseEnter={(e) => { if (sendMode !== key) e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = sendMode === key ? colors.selectedBg : "transparent"; }}
                >
                  <span style={{ fontWeight: 600 }}>{label}{sendMode === key ? " \u2713" : ""}</span>
                  <span style={{ fontSize: 10, color: colors.textDim }}>{desc}</span>
                </button>
              ))}
              <div style={{ padding: "4px 8px 2px", fontSize: 10, color: colors.textDim, borderTop: `1px solid ${colors.border}`, marginTop: 2 }}>
                {navigator.platform.includes("Mac") ? "⌘" : "Ctrl+"}Enter flips mode for one send
              </div>
            </div>
          </>
        )}
      </div>
      </div>
    </div>
  );
}
