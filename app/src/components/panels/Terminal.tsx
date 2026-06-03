import { useCallback, useEffect, useRef, useState } from "react";
import type { GateApprovalRequestedData, SessionStatus, TerminalTarget } from "../../types";
import type { AgentOpenMode } from "../../types/panels";
import { fetchRoots, type RootEntry } from "../../api/files";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { useTerminalWs } from "../../hooks/useTerminalWs";
import { useElapsedTimer } from "../../hooks/useElapsedTimer";
import { useXTerminal } from "../../hooks/useXTerminal";
import { TerminalToolbar } from "./TerminalToolbar";
import { PaneHeaderStatus } from "./PaneHeaderStatus";
import { TerminalShortcuts } from "./TerminalShortcuts";
import { ApprovalCard } from "../chat/ApprovalCard";

/** Module-level registry so TerminalPanes can call sendClose for a specific instance. */
const closeRegistry = new Map<string, () => void>();

/** Idle window (no bytes from the container) we wait for after a deny-with-
 * prompt paste before submitting Enter. The TUI emits bytes while it's
 * handling the deny tool result and redrawing the input box; once it's
 * quiescent for this long, it's back at the prompt and ready to accept \r. */
const DENY_SUBMIT_QUIESCENCE_MS = 400;

export function getCloseForInstance(key: string): (() => void) | undefined {
  return closeRegistry.get(key);
}

interface TerminalProps {
  channelId: string | null;
  target?: TerminalTarget;
  instanceId?: string;
  /** Claude Code session ID to resume (overrides the channel's stored session). */
  claudeSessionId?: string;
  /** Start a fresh session, ignoring the channel's stored session. */
  newSession?: boolean;
  /** How the agent terminal should boot Claude relative to the channel's session.
   *  Only meaningful when target === "agent" and no claudeSessionId override is set. */
  openMode?: AgentOpenMode;
  /** Explicit command to run instead of the interactive Claude bootstrap. */
  cmd?: string[];
  /** Hide Kill/Restart from the toolbar (used when a parent provides these). */
  hideActions?: boolean;
  /** Incrementing this value triggers sendKill from the parent. */
  killSignal?: number;
  onStatusChange?: () => void;
  /** Reports session status changes to the parent (e.g. for aggregate Kill/Restart). */
  onPaneStatus?: (status: SessionStatus) => void;
  onSessionEnd?: () => void;
  /** When set, overlays the chat's ApprovalCard on top of the terminal content. */
  gateApproval?: GateApprovalRequestedData | null;
  /** Called after the user clicks Allow/Deny so the parent can clear gateApproval. */
  onGateApprovalResolved?: () => void;
}

export function Terminal({ channelId, target = "agent", instanceId, claudeSessionId, newSession, openMode, cmd, hideActions, killSignal, onStatusChange, onPaneStatus, onSessionEnd, gateApproval, onGateApprovalResolved }: TerminalProps) {
  const { colors, fontSizes } = useTheme();
  const terminalRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const { elapsed, start, stop, reset } = useElapsedTimer();

  // Shell panes (host shell or docker shell) can open in any workspace root.
  // The agent (Claude) pane — target "agent" with no explicit cmd — is excluded.
  const isShell = target === "host" || (!!cmd && cmd.length > 0);
  const [roots, setRoots] = useState<RootEntry[]>([]);
  const [rootIndex, setRootIndex] = useState(0);

  useEffect(() => {
    setRootIndex(0);
    if (!channelId || !isShell) { setRoots([]); return; }
    let cancelled = false;
    fetchRoots(channelId)
      .then((r) => { if (!cancelled) setRoots(r); })
      .catch(() => { if (!cancelled) setRoots([]); });
    return () => { cancelled = true; };
  }, [channelId, isShell]);

  const getStartTimeRef = useRef<(() => number | undefined) | null>(null);

  // Stable handle so onData (defined before useTerminalWs) can call sendInput
  // for the deny-with-prompt quiescence-fire.
  const sendInputRef = useRef<((data: string) => void) | null>(null);

  // When non-null, a deny-with-prompt submit is armed: each byte from the
  // container resets idleTimer; when idleTimer fires (= TUI quiet for
  // QUIESCENCE_MS), we send \r to submit the pasted text.
  const pendingSubmitRef = useRef<{ idleTimer: ReturnType<typeof setTimeout> | null } | null>(null);

  const onData = useCallback((data: ArrayBuffer) => {
    writeRef.current?.(new Uint8Array(data));
    const ps = pendingSubmitRef.current;
    if (ps) {
      if (ps.idleTimer) clearTimeout(ps.idleTimer);
      ps.idleTimer = setTimeout(() => {
        pendingSubmitRef.current = null;
        sendInputRef.current?.("\r");
      }, DENY_SUBMIT_QUIESCENCE_MS);
    }
  }, []);

  const onStatus = useCallback(
    (newStatus: SessionStatus) => {
      setStatus(newStatus);
      if (newStatus === "running") {
        start(getStartTimeRef.current?.());
      }
      if (newStatus === "completed" || newStatus === "failed") {
        stop();
        // Reset terminal mouse tracking modes that the killed process may not
        // have cleaned up.  Without this, mouse movements generate raw escape
        // sequences that the shell interprets as text input.
        writeRef.current?.(
          "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l",
        );
        onSessionEnd?.();
      }
      onStatusChange?.();
      onPaneStatus?.(newStatus);
    },
    [start, stop, onStatusChange, onPaneStatus],
  );

  const onError = useCallback((message: string) => {
    writeRef.current?.(
      new TextEncoder().encode(`\r\n\x1b[31m[error] ${message}\x1b[0m\r\n`),
    );
  }, []);

  // Ref to access xterm dimensions when sending create/attach messages.
  const xtermInstRef = useRef<import("@xterm/xterm").Terminal | null>(null);

  const { sendInput, sendResize, sendKill, sendClose, sendCreate, getStartTime } = useTerminalWs({
    channelId,
    target,
    instanceId,
    claudeSessionId,
    newSession,
    openMode,
    cmd,
    rootIndex,
    onData,
    onStatus,
    onError,
    getTerminalSize: () => {
      const term = xtermInstRef.current;
      return term ? { cols: term.cols, rows: term.rows } : null;
    },
  });

  getStartTimeRef.current = getStartTime;
  sendInputRef.current = sendInput;

  // Cancel any pending deny-with-prompt submit when the pane unmounts.
  useEffect(() => () => {
    const ps = pendingSubmitRef.current;
    if (ps?.idleTimer) clearTimeout(ps.idleTimer);
    pendingSubmitRef.current = null;
  }, []);

  // Register sendClose so TerminalPanes can call it when explicitly closing a pane.
  const registryKey = `${target}:${channelId}:${instanceId}`;
  const sendCloseRef = useRef(sendClose);
  sendCloseRef.current = sendClose;
  useEffect(() => {
    const key = registryKey;
    closeRegistry.set(key, () => sendCloseRef.current());
    return () => { closeRegistry.delete(key); };
  }, [registryKey]);

  // Kill when killSignal increments from parent.
  const killSignalRef = useRef(killSignal ?? 0);
  const sendKillRef = useRef(sendKill);
  sendKillRef.current = sendKill;
  useEffect(() => {
    const prev = killSignalRef.current;
    killSignalRef.current = killSignal ?? 0;
    if ((killSignal ?? 0) > prev) {
      sendKillRef.current();
    }
  }, [killSignal]);

  const handleRestart = useCallback(() => {
    reset();
    sendCreate();
  }, [reset, sendCreate]);

  // Switching workspace root re-creates the session in the new dir. Skip the
  // initial mount (the first create already carries the current rootIndex).
  // Reset the xterm first so the new shell's prompt starts on a clean screen
  // instead of being appended to the previous session's dangling prompt line.
  const sendCreateRef = useRef(sendCreate);
  sendCreateRef.current = sendCreate;
  const prevRootIndexRef = useRef(rootIndex);
  useEffect(() => {
    if (prevRootIndexRef.current === rootIndex) return;
    prevRootIndexRef.current = rootIndex;
    reset();
    xtermInstRef.current?.reset();
    sendCreateRef.current();
  }, [rootIndex, reset]);

  const handleShortcutPick = useCallback((text: string) => {
    if (target === "agent" && !cmd) {
      // Claude TUI: bracketed paste so multi-line prompts (e.g. file-backed
      // shortcuts) land as a single paste buffer instead of one Enter-per-
      // newline; trailing \r submits.
      sendInput(`\x1b[200~${text}\x1b[201~\r`);
    } else {
      // Raw shell (host or docker-shell). Bash doesn't enable bracketed paste
      // by default, so the \x1b[200~ … \x1b[201~ wrappers would leak through
      // as literal text. Send the command + newline to execute immediately.
      sendInput(`${text}\n`);
    }
  }, [sendInput, target, cmd]);

  const { write, xtermRef } = useXTerminal({
    containerRef: terminalRef,
    colors,
    fontSize: fontSizes.terminal,
    onInput: sendInput,
    onResize: sendResize,
  });
  xtermInstRef.current = xtermRef.current;

  // Stable ref so callbacks created before useXTerminal can access write.
  const writeRef = useRef(write);
  writeRef.current = write;

  if (!channelId) {
    return (
      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: colors.textDim,
          fontSize: 14,
        }}
      >
        Select a channel to open terminal
      </div>
    );
  }

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden" }}>
      {hideActions && instanceId ? (
        <PaneHeaderStatus leafId={instanceId} status={status} elapsed={elapsed} />
      ) : (
        <TerminalToolbar
          status={status}
          elapsed={elapsed}
          onKill={sendKill}
          onRestart={handleRestart}
          killLabel={target === "host" ? "Close" : "Stop"}
          killTitle={target === "host" ? "Close shell session" : "Stop container and end session"}
        />
      )}
      {isShell && roots.length > 1 && (
        <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 6, padding: "2px 8px", borderBottom: `1px solid ${colors.border}`, flexShrink: 0 }}>
          <span style={{ fontSize: 10, color: colors.textDim }}>root</span>
          <select
            value={rootIndex}
            onChange={(e) => setRootIndex(Number(e.target.value))}
            title="Workspace root the shell opens in"
            data-testid="terminal-root-select"
            style={{
              background: colors.surface,
              color: colors.textLight,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              padding: "1px 4px",
              outline: "none",
              maxWidth: 160,
              cursor: "pointer",
            }}
          >
            {roots.map((r) => (
              <option key={r.index} value={r.index} title={r.path}>{r.path}</option>
            ))}
          </select>
        </div>
      )}
      <div style={{ flex: 1, position: "relative", overflow: "hidden", minHeight: 0 }}>
        <div style={{ padding: "8px 0 8px 12px", width: "100%", height: "100%", boxSizing: "border-box" }}>
          <div ref={terminalRef} style={{ width: "100%", height: "100%" }} />
        </div>
        {gateApproval && channelId && (
          <div style={{
            position: "absolute",
            inset: 0,
            backgroundColor: "rgba(0, 0, 0, 0.55)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: 16,
            zIndex: 10,
          }}>
            <div style={{ width: "100%", maxWidth: 520, boxShadow: "0 8px 24px rgba(0,0,0,0.35)", borderRadius: 8 }}>
              <ApprovalCard
                data={gateApproval}
                channelId={channelId}
                onResolved={onGateApprovalResolved}
                onDenyWithPrompt={(text) => {
                  // Bracketed-paste so multi-line prompts arrive atomically
                  // in the TUI. Routes the typed text into THIS pane's stdin
                  // instead of posting to chat.
                  sendInput(`\x1b[200~${text}\x1b[201~`);
                  // Arm the quiescence-based submit instead of using a fixed
                  // timeout: onData resets the idle timer on every byte from
                  // the container, so \r fires the moment the TUI stops
                  // emitting output (= back at the input prompt).
                  pendingSubmitRef.current = { idleTimer: null };
                }}
                style={{ margin: 0 }}
              />
            </div>
          </div>
        )}
      </div>
      {instanceId && (
        <TerminalShortcuts
          channelId={channelId}
          leafId={instanceId}
          onPick={handleShortcutPick}
          target={target}
          showPrompts={target === "agent" && !cmd}
        />
      )}
    </div>
  );
}
