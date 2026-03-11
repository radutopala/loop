import { useCallback, useRef, useState } from "react";
import type { SessionStatus, TerminalTarget } from "../types";
import { colors } from "../theme";
import { useTerminalWs } from "../hooks/useTerminalWs";
import { useElapsedTimer } from "../hooks/useElapsedTimer";
import { useXTerminal } from "../hooks/useXTerminal";
import { TerminalToolbar } from "./TerminalToolbar";

interface TerminalProps {
  channelId: string | null;
  target?: TerminalTarget;
  onStatusChange?: () => void;
  onSessionEnd?: () => void;
}

export function Terminal({ channelId, target = "agent", onStatusChange, onSessionEnd }: TerminalProps) {
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
        onSessionEnd?.();
      }
      onStatusChange?.();
    },
    [start, stop, onStatusChange],
  );

  const onError = useCallback((message: string) => {
    writeRef.current?.(
      new TextEncoder().encode(`\r\n\x1b[31m[error] ${message}\x1b[0m\r\n`),
    );
  }, []);

  // Ref to access xterm dimensions when sending create/attach messages.
  const xtermInstRef = useRef<import("@xterm/xterm").Terminal | null>(null);

  const { sendInput, sendResize, sendKill, sendCreate, getStartTime } = useTerminalWs({
    channelId,
    target,
    onData,
    onStatus,
    onError,
    getTerminalSize: () => {
      const term = xtermInstRef.current;
      return term ? { cols: term.cols, rows: term.rows } : null;
    },
  });

  getStartTimeRef.current = getStartTime;

  const handleRestart = useCallback(() => {
    reset();
    sendCreate();
  }, [reset, sendCreate]);

  const { write, xtermRef } = useXTerminal({
    containerRef: terminalRef,
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
        onKill={sendKill}
        onRestart={handleRestart}
        killLabel={target === "host" ? "Close" : "Kill"}
        killTitle={target === "host" ? "Close shell session" : "Kill session and remove container"}
      />
      <div style={{ flex: 1, position: "relative", overflow: "hidden" }}>
        <div style={{ padding: "8px 12px", width: "100%", height: "100%", boxSizing: "border-box" }}>
          <div ref={terminalRef} style={{ width: "100%", height: "100%" }} />
        </div>
      </div>
    </div>
  );
}
