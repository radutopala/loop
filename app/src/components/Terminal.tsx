import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionStatus } from "../types";
import { colors, fonts } from "../theme";
import { useTerminalWs } from "../hooks/useTerminalWs";
import { TerminalToolbar } from "./TerminalToolbar";

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
        fontFamily: fonts.mono,
        theme: {
          background: colors.bg,
          foreground: colors.text,
          cursor: colors.cursor,
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
