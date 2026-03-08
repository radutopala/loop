import { useCallback, useRef, useState } from "react";
import type { SessionStatus } from "../types";
import { colors } from "../theme";
import { useTerminalWs } from "../hooks/useTerminalWs";
import { useElapsedTimer } from "../hooks/useElapsedTimer";
import { useXTerminal } from "../hooks/useXTerminal";
import { TerminalToolbar } from "./TerminalToolbar";

interface TerminalProps {
  channelId: string | null;
  containerId: string | null;
}

export function Terminal({ channelId, containerId }: TerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const { elapsed, start, stop } = useElapsedTimer();

  const onData = useCallback((data: ArrayBuffer) => {
    writeRef.current?.(new Uint8Array(data));
  }, []);

  const onStatus = useCallback(
    (newStatus: SessionStatus) => {
      setStatus(newStatus);
      if (newStatus === "running") start();
      if (newStatus === "completed" || newStatus === "failed") stop();
    },
    [start, stop],
  );

  const onError = useCallback((message: string) => {
    writeRef.current?.(
      new TextEncoder().encode(`\r\n\x1b[31m[error] ${message}\x1b[0m\r\n`),
    );
  }, []);

  const { sendInput, sendResize, sendStop } = useTerminalWs({
    channelId,
    containerId,
    onData,
    onStatus,
    onError,
  });

  const { write } = useXTerminal({
    containerRef: terminalRef,
    onInput: sendInput,
    onResize: sendResize,
  });

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
        Select a project to open terminal
      </div>
    );
  }

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
      <TerminalToolbar status={status} elapsed={elapsed} onStop={sendStop} />
      <div ref={terminalRef} style={{ flex: 1 }} />
    </div>
  );
}
