import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionStatus, TerminalTarget } from "../../types";
import { useTheme } from "../../ThemeContext";
import { useTerminalWs } from "../../hooks/useTerminalWs";
import { useElapsedTimer } from "../../hooks/useElapsedTimer";
import { useXTerminal } from "../../hooks/useXTerminal";
import { TerminalToolbar } from "./TerminalToolbar";

/** Module-level registry so TerminalPanes can call sendClose for a specific instance. */
const closeRegistry = new Map<string, () => void>();

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
  /** Hide Kill/Restart from the toolbar (used when a parent provides these). */
  hideActions?: boolean;
  /** Incrementing this value triggers sendKill from the parent. */
  killSignal?: number;
  onStatusChange?: () => void;
  /** Reports session status changes to the parent (e.g. for aggregate Kill/Restart). */
  onPaneStatus?: (status: SessionStatus) => void;
  onSessionEnd?: () => void;
}

export function Terminal({ channelId, target = "agent", instanceId, claudeSessionId, newSession, hideActions, killSignal, onStatusChange, onPaneStatus, onSessionEnd }: TerminalProps) {
  const { colors, fontSizes } = useTheme();
  const terminalRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const { elapsed, start, stop, reset } = useElapsedTimer();

  const getStartTimeRef = useRef<(() => number | undefined) | null>(null);

  const onData = useCallback((data: ArrayBuffer) => {
    writeRef.current?.(new Uint8Array(data));
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
    onData,
    onStatus,
    onError,
    getTerminalSize: () => {
      const term = xtermInstRef.current;
      return term ? { cols: term.cols, rows: term.rows } : null;
    },
  });

  getStartTimeRef.current = getStartTime;

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
      <TerminalToolbar
        status={status}
        elapsed={elapsed}
        onKill={hideActions ? undefined : sendKill}
        onRestart={hideActions ? undefined : handleRestart}
        killLabel={target === "host" ? "Close" : "Kill"}
        killTitle={target === "host" ? "Close shell session" : "Kill session and remove container"}
      />
      <div style={{ flex: 1, position: "relative", overflow: "hidden", minHeight: 0 }}>
        <div style={{ padding: "8px 0 8px 12px", width: "100%", height: "100%", boxSizing: "border-box" }}>
          <div ref={terminalRef} style={{ width: "100%", height: "100%" }} />
        </div>
      </div>
    </div>
  );
}
