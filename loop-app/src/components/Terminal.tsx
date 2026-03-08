import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionStatus } from "../types";
import { useTerminalWs } from "../hooks/useTerminalWs";
import { StatusBadge } from "./StatusBadge";

interface TerminalProps {
  channelId: string | null;
  containerId: string | null;
}

export function Terminal({ channelId, containerId }: TerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<import("@xterm/xterm").Terminal | null>(null);
  const fitAddonRef =
    useRef<import("@xterm/addon-fit").FitAddon | null>(null);
  const [status, setStatus] = useState<SessionStatus>("connecting");
  const [elapsed, setElapsed] = useState(0);
  const startTimeRef = useRef<number | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval>>(undefined);

  const onData = useCallback((data: ArrayBuffer) => {
    xtermRef.current?.write(new Uint8Array(data));
  }, []);

  const onStatus = useCallback((newStatus: SessionStatus) => {
    setStatus(newStatus);
    if (newStatus === "running" && !startTimeRef.current) {
      startTimeRef.current = Date.now();
      timerRef.current = setInterval(() => {
        if (startTimeRef.current) {
          setElapsed(Math.floor((Date.now() - startTimeRef.current) / 1000));
        }
      }, 1000);
    }
    if (newStatus === "completed" || newStatus === "failed") {
      clearInterval(timerRef.current);
    }
  }, []);

  const onError = useCallback((message: string) => {
    xtermRef.current?.write(`\r\n\x1b[31m[error] ${message}\x1b[0m\r\n`);
  }, []);

  const { sendInput, sendResize, sendStop } = useTerminalWs({
    channelId,
    containerId,
    onData,
    onStatus,
    onError,
  });

  useEffect(() => {
    if (!terminalRef.current) return;

    let disposed = false;

    async function init() {
      const { Terminal: XTerm } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      const { WebLinksAddon } = await import("@xterm/addon-web-links");

      if (disposed) return;

      const term = new XTerm({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
        theme: {
          background: "#1a1b26",
          foreground: "#a9b1d6",
          cursor: "#c0caf5",
        },
      });

      const fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.loadAddon(new WebLinksAddon());

      term.open(terminalRef.current!);
      fitAddon.fit();

      xtermRef.current = term;
      fitAddonRef.current = fitAddon;

      sendResize(term.cols, term.rows);

      term.onData((data) => {
        sendInput(data);
      });

      term.onResize(({ cols, rows }) => {
        sendResize(cols, rows);
      });

      const resizeObserver = new ResizeObserver(() => {
        fitAddon.fit();
      });
      resizeObserver.observe(terminalRef.current!);

      return () => {
        resizeObserver.disconnect();
      };
    }

    const cleanup = init();

    return () => {
      disposed = true;
      cleanup.then((fn) => fn?.());
      xtermRef.current?.dispose();
      xtermRef.current = null;
      fitAddonRef.current = null;
      clearInterval(timerRef.current);
      startTimeRef.current = null;
      setElapsed(0);
    };
  }, [channelId, sendInput, sendResize]);

  const formatElapsed = (s: number) => {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
  };

  if (!channelId) {
    return (
      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "#6b7280",
          fontSize: 14,
        }}
      >
        Select a project to open terminal
      </div>
    );
  }

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          padding: "8px 16px",
          borderBottom: "1px solid #2d2d2d",
          backgroundColor: "#1e1e2e",
        }}
      >
        <StatusBadge status={status} />
        <button
          onClick={sendStop}
          disabled={status !== "running"}
          style={{
            padding: "4px 12px",
            borderRadius: 6,
            border: "1px solid #ef4444",
            backgroundColor: "transparent",
            color: status === "running" ? "#ef4444" : "#4b5563",
            cursor: status === "running" ? "pointer" : "default",
            fontSize: 12,
          }}
        >
          Stop
        </button>
        <span style={{ color: "#9ca3af", fontSize: 12, fontFamily: "monospace" }}>
          {formatElapsed(elapsed)}
        </span>
      </div>
      <div ref={terminalRef} style={{ flex: 1 }} />
    </div>
  );
}
