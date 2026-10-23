import { useRef, useState } from "react";
import type { RootEntry } from "../../api/files";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ContextMenu } from "../shared/ContextMenu";

interface RootPickerProps {
  roots: RootEntry[];
  value: number;
  onChange: (index: number) => void;
  /** Stable test id for the trigger button. */
  testId: string;
  title?: string;
}

/**
 * A workspace-root chooser: a compact button showing the current root's
 * path, opening a themed menu that lists each root by name with its path
 * beside it and the current one checked.
 */
export function RootPicker({ roots, value, onChange, testId, title }: RootPickerProps) {
  const { colors } = useTheme();
  const btnRef = useRef<HTMLButtonElement>(null);
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const current = roots.find((r) => r.index === value) ?? roots[0];

  const open = () => {
    const el = btnRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setMenuPos({ x: r.left, y: r.bottom + 2 });
  };

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        onClick={open}
        title={current ? `${title ? `${title}\n` : ""}${current.path}` : title}
        aria-haspopup="menu"
        aria-expanded={!!menuPos}
        data-testid={testId}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 4,
          background: colors.surface,
          color: colors.textLight,
          border: `1px solid ${colors.border}`,
          borderRadius: 4,
          fontSize: 10,
          fontFamily: fonts.mono,
          padding: "0 4px 0 5px",
          maxWidth: 180,
          cursor: "pointer",
          outline: "none",
        }}
      >
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{current ? current.display_path || current.path : ""}</span>
        <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0 }}>
          <polyline points="6,9 12,15 18,9" />
        </svg>
      </button>
      {menuPos && (
        <ContextMenu
          x={menuPos.x}
          y={menuPos.y}
          onClose={() => setMenuPos(null)}
          items={roots.map((r) => ({
            label: r.name,
            hint: r.display_path || r.path,
            title: r.path,
            checked: r.index === value,
            onClick: () => onChange(r.index),
          }))}
        />
      )}
    </>
  );
}
