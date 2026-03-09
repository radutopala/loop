import { useCallback, useRef, useState } from "react";
import type { SessionStatus } from "../types";
import { colors } from "../theme";
import { useTerminalWs } from "../hooks/useTerminalWs";
import { useElapsedTimer } from "../hooks/useElapsedTimer";
import { useXTerminal } from "../hooks/useXTerminal";
import { TerminalToolbar } from "./TerminalToolbar";

interface TerminalProps {
  channelId: string | null;
  onStatusChange?: () => void;
}

export function Terminal({ channelId, onStatusChange }: TerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const [detached, setDetached] = useState(false);
  const { elapsed, start, stop, resume } = useElapsedTimer();

  const onData = useCallback((data: ArrayBuffer) => {
    writeRef.current?.(new Uint8Array(data));
  }, []);

  const onStatus = useCallback(
    (newStatus: SessionStatus) => {
      setStatus(newStatus);
      if (newStatus === "running") {
        start();
        setDetached(false);
      }
      if (newStatus === "completed" || newStatus === "failed") {
        stop();
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

  const { sendInput, sendResize, sendKill, sendDetach, sendReattach } = useTerminalWs({
    channelId,
    onData,
    onStatus,
    onError,
    getTerminalSize: () => {
      const term = xtermInstRef.current;
      return term ? { cols: term.cols, rows: term.rows } : null;
    },
  });

  const { write, clear, xtermRef } = useXTerminal({
    containerRef: terminalRef,
    onInput: sendInput,
    onResize: sendResize,
  });
  xtermInstRef.current = xtermRef.current;

  // Stable ref so callbacks created before useXTerminal can access write.
  const writeRef = useRef(write);
  writeRef.current = write;

  const clearRef = useRef(clear);
  clearRef.current = clear;

  const handleDetach = useCallback(() => {
    sendDetach();
    setDetached(true);
  }, [sendDetach]);

  const handleReattach = useCallback(() => {
    clearRef.current();
    setDetached(false);
    resume();
    sendReattach();
  }, [sendReattach, resume]);

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
    <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
      <TerminalToolbar
        status={status}
        elapsed={elapsed}
        detached={detached}
        onKill={sendKill}
        onDetach={handleDetach}
        onReattach={handleReattach}
      />
      <div style={{ flex: 1, position: "relative", overflow: "hidden" }}>
        <div style={{ padding: "8px 12px", width: "100%", height: "100%", boxSizing: "border-box" }}>
          <div ref={terminalRef} style={{ width: "100%", height: "100%" }} />
        </div>
        {detached && (
          <div
            style={{
              position: "absolute",
              inset: 0,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              gap: 12,
              backgroundColor: "rgba(0, 0, 0, 0.8)",
              color: colors.textDim,
              fontSize: 14,
              zIndex: 1,
            }}
          >
            <span>Session detached — still running in background</span>
            <button
              onClick={handleReattach}
              style={{
                padding: "6px 16px",
                borderRadius: 6,
                border: `1px solid ${colors.active}`,
                backgroundColor: "transparent",
                color: colors.active,
                cursor: "pointer",
                fontSize: 13,
              }}
            >
              Reattach
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
