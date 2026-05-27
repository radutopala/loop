import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { fetchShortcuts, fetchBashShortcuts, type PromptShortcut, type BashShortcut } from "../../api/configApi";
import type { TerminalTarget } from "../../types";

interface TerminalShortcutsProps {
  channelId: string;
  /** Pane leaf id — used for test selectors. */
  leafId: string;
  /** Called with the resolved text when a shortcut is picked. */
  onPick: (text: string) => void;
  /** Which terminal this footer is attached to. */
  target?: TerminalTarget;
  /** True when the pane runs Claude (so # prompt shortcuts apply). Defaults
   *  to true for `target === "agent"`. The Docker shell pane runs raw bash in
   *  the agent container and passes false so only $ bash shortcuts surface. */
  showPrompts?: boolean;
}

type MenuKind = "prompt" | "bash";

/**
 * Footer bar rendered below the xterm content. Shows a `#` button for prompt
 * shortcuts (agent only) and a `$` button for bash shortcuts (both targets).
 * Each opens a dropdown popping upward.
 */
export function TerminalShortcuts({ channelId, leafId, onPick, target = "agent", showPrompts }: TerminalShortcutsProps) {
  const promptsEnabled = showPrompts ?? (target === "agent");
  const { colors } = useTheme();
  const [prompts, setPrompts] = useState<PromptShortcut[]>([]);
  const [bash, setBash] = useState<BashShortcut[]>([]);
  const [openKind, setOpenKind] = useState<MenuKind | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [menuPos, setMenuPos] = useState<{ bottom: number; left: number }>({ bottom: 0, left: 0 });
  const promptBtnRef = useRef<HTMLButtonElement>(null);
  const bashBtnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    if (promptsEnabled) {
      fetchShortcuts(channelId)
        .then((list) => { if (!cancelled) setPrompts(list); })
        .catch(() => { if (!cancelled) setPrompts([]); });
    } else {
      fetchBashShortcuts(channelId)
        .then((list) => { if (!cancelled) setBash(list); })
        .catch(() => { if (!cancelled) setBash([]); });
    }
    return () => { cancelled = true; };
  }, [channelId, promptsEnabled]);

  useLayoutEffect(() => {
    if (openKind === null) return;
    const btn = openKind === "prompt" ? promptBtnRef.current : bashBtnRef.current;
    if (!btn) return;
    const r = btn.getBoundingClientRect();
    setMenuPos({ bottom: window.innerHeight - r.top + 4, left: r.left });
  }, [openKind]);

  useEffect(() => {
    if (openKind === null) return;
    const handler = (e: MouseEvent) => {
      const t = e.target as Node;
      if (promptBtnRef.current?.contains(t)) return;
      if (bashBtnRef.current?.contains(t)) return;
      if (menuRef.current?.contains(t)) return;
      setOpenKind(null);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [openKind]);

  const pickPrompt = useCallback((sc: PromptShortcut) => {
    setOpenKind(null);
    onPick(sc.prompt);
  }, [onPick]);

  const pickBash = useCallback((sc: BashShortcut) => {
    setOpenKind(null);
    onPick(sc.command);
  }, [onPick]);

  const showPromptBtn = promptsEnabled && prompts.length > 0;
  const showBashBtn = !promptsEnabled && bash.length > 0;

  if (!showPromptBtn && !showBashBtn) return null;

  const items: { name: string; description: string; text: string }[] =
    openKind === "prompt"
      ? prompts.map((p) => ({ name: p.name, description: p.description, text: p.prompt }))
      : openKind === "bash"
        ? bash.map((b) => ({ name: b.name, description: b.description, text: b.command }))
        : [];

  const pickItem = (idx: number) => {
    if (openKind === "prompt") {
      const p = prompts[idx];
      if (p) pickPrompt(p);
    } else if (openKind === "bash") {
      const b = bash[idx];
      if (b) pickBash(b);
    }
  };

  const sigil = openKind === "bash" ? "$" : "#";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "6px 12px",
        borderTop: `1px solid ${colors.border}`,
        backgroundColor: colors.surface,
        flexShrink: 0,
        height: 40,
      }}
    >
      {showPromptBtn && (
        <button
          ref={promptBtnRef}
          onClick={() => { setSelectedIdx(0); setOpenKind((k) => (k === "prompt" ? null : "prompt")); }}
          title="Prompt shortcuts"
          data-testid={`terminal-shortcuts-btn-${leafId}`}
          style={pickerBtnStyle(colors)}
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
      )}
      {showBashBtn && (
        <button
          ref={bashBtnRef}
          onClick={() => { setSelectedIdx(0); setOpenKind((k) => (k === "bash" ? null : "bash")); }}
          title="Bash shortcuts"
          data-testid={`terminal-bash-shortcuts-btn-${leafId}`}
          style={pickerBtnStyle(colors)}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = colors.hoverBg;
            e.currentTarget.style.color = colors.textLight;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = "transparent";
            e.currentTarget.style.color = colors.textDim;
          }}
        >
          $
        </button>
      )}
      {openKind !== null && createPortal(
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
            {items.map((it, i) => (
              <div
                key={it.name}
                onMouseDown={(e) => { e.preventDefault(); pickItem(i); }}
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
                  {sigil}{it.name}
                </div>
                <div style={{ color: colors.textMuted, fontSize: 12, fontFamily: fonts.sans }}>
                  {it.description}
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

function pickerBtnStyle(colors: ReturnType<typeof useTheme>["colors"]): React.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    width: 28,
    height: 28,
    padding: 0,
    background: "transparent",
    border: `1px solid ${colors.border}`,
    borderRadius: 8,
    color: colors.textDim,
    cursor: "pointer",
    fontFamily: fonts.mono,
    fontSize: 14,
    fontWeight: 600,
    lineHeight: 1,
    flexShrink: 0,
  };
}
