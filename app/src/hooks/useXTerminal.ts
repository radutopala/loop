import { useCallback, useEffect, useRef } from "react";
import type { RefObject } from "react";
import { colors, fonts } from "../theme";

interface UseXTerminalOptions {
  containerRef: RefObject<HTMLDivElement | null>;
  onInput: (data: string) => void;
  onResize: (cols: number, rows: number) => void;
}

/**
 * Manages xterm.js lifecycle: lazy init, fit, resize observation, and cleanup.
 * Returns a `write` callback for pushing data into the terminal and a ref to
 * the underlying Terminal instance.
 */
export function useXTerminal({
  containerRef,
  onInput,
  onResize,
}: UseXTerminalOptions) {
  const xtermRef = useRef<import("@xterm/xterm").Terminal | null>(null);

  const write = useCallback((data: Uint8Array | string) => {
    xtermRef.current?.write(data);
  }, []);

  useEffect(() => {
    if (!containerRef.current) return;

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

      term.open(containerRef.current!);
      fitAddon.fit();

      xtermRef.current = term;

      onResize(term.cols, term.rows);

      term.onData((data) => {
        onInput(data);
      });

      term.onResize(({ cols, rows }) => {
        onResize(cols, rows);
      });

      const resizeObserver = new ResizeObserver(() => {
        fitAddon.fit();
      });
      resizeObserver.observe(containerRef.current!);

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
    };
  }, [containerRef, onInput, onResize]);

  return { write, xtermRef };
}
