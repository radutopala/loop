import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { fetchShortcuts, type PromptShortcut } from "../../api/configApi";
import { LoopInfinityIcon } from "../LoopInfinityIcon";
import type { SessionStatus } from "../../types";

interface TerminalShortcutsProps {
  channelId: string;
  /** Pane leaf id — used for test selectors. */
  leafId: string;
  /** Called with the resolved prompt text when a shortcut is picked. */
  onPick: (prompt: string) => void;
  /** Current session status — drives the animated isolation logo. */
  status?: SessionStatus;
}

/**
 * Footer bar rendered below the xterm content. Shows a `#` button that
 * opens a dropdown (popping upward) of prompt shortcuts.
 */
export function TerminalShortcuts({ channelId, leafId, onPick, status }: TerminalShortcutsProps) {
  const { colors } = useTheme();
  const [shortcuts, setShortcuts] = useState<PromptShortcut[]>([]);
  const [open, setOpen] = useState(false);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [menuPos, setMenuPos] = useState<{ bottom: number; left: number }>({ bottom: 0, left: 0 });
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    fetchShortcuts(channelId)
      .then((list) => { if (!cancelled) setShortcuts(list); })
      .catch(() => { if (!cancelled) setShortcuts([]); });
    return () => { cancelled = true; };
  }, [channelId]);

  useLayoutEffect(() => {
    if (!open || !btnRef.current) return;
    const r = btnRef.current.getBoundingClientRect();
    // Pop upward: anchor menu's bottom 4px above the button's top.
    setMenuPos({ bottom: window.innerHeight - r.top + 4, left: r.left });
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (btnRef.current?.contains(target)) return;
      if (menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const pick = useCallback((sc: PromptShortcut) => {
    setOpen(false);
    onPick(sc.prompt);
  }, [onPick]);

  const isRunning = status === "running";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "2px 8px",
        borderTop: `1px solid ${colors.border}`,
        backgroundColor: colors.surface,
        flexShrink: 0,
        height: 22,
      }}
    >
      {shortcuts.length > 0 ? (
        <button
          ref={btnRef}
          onClick={() => { setSelectedIdx(0); setOpen((v) => !v); }}
          title="Prompt shortcuts"
          data-testid={`terminal-shortcuts-btn-${leafId}`}
          style={{
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            width: 18,
            height: 18,
            padding: 0,
            background: "transparent",
            border: "none",
            borderRadius: 4,
            color: colors.textDim,
            cursor: "pointer",
            fontFamily: fonts.mono,
            fontSize: 12,
            fontWeight: 600,
            lineHeight: 1,
            flexShrink: 0,
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = colors.hoverBg;
            e.currentTarget.style.color = colors.textLight;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = "transparent";
            e.currentTarget.style.color = colors.textDim;
          }}
        >
          #
        </button>
      ) : null}
      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          gap: 6,
          fontSize: 11,
          color: colors.textDim,
          fontFamily: fonts.mono,
        }}
      >
        <LoopInfinityIcon color={isRunning ? undefined : colors.textDim} animated={isRunning} isDark={colors.isDark} />
        Running interactively in an isolated Docker container
      </div>
      {open && createPortal(
        <div
          ref={menuRef}
          data-testid={`terminal-shortcuts-menu-${leafId}`}
          style={{
            position: "fixed",
            bottom: menuPos.bottom,
            left: menuPos.left,
            zIndex: 1000,
            backgroundColor: colors.sidebar,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            padding: "6px 0",
            minWidth: 240,
            maxHeight: 280,
            overflow: "hidden",
            boxShadow: `0 4px 12px ${colors.shadow}`,
          }}
        >
          <div style={{ maxHeight: 268, overflowY: "auto", padding: "0 4px" }}>
            {shortcuts.map((sc, i) => (
              <div
                key={sc.name}
                onMouseDown={(e) => { e.preventDefault(); pick(sc); }}
                onMouseEnter={() => setSelectedIdx(i)}
                style={{
                  padding: "8px 12px",
                  borderRadius: 6,
                  cursor: "pointer",
                  display: "flex",
                  flexDirection: "column",
                  gap: 2,
                  backgroundColor: i === selectedIdx ? colors.selectedBg : "transparent",
                }}
              >
                <div style={{ color: colors.textLight, fontWeight: 600, fontSize: 13, fontFamily: fonts.mono }}>
                  #{sc.name}
                </div>
                <div style={{ color: colors.textMuted, fontSize: 12, fontFamily: fonts.sans }}>
                  {sc.description}
                </div>
              </div>
            ))}
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
}
