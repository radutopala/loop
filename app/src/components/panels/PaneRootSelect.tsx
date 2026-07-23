import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RootEntry } from "../../api/files";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

interface PaneRootSelectProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  roots: RootEntry[];
  value: number;
  onChange: (index: number) => void;
  /** Stable test id for the rendered <select>. */
  testId: string;
  title?: string;
}

/**
 * Renders a workspace-root selector into a pane's header slot (next to the
 * status pill), mirroring how PaneHeaderStatus portals itself. Shared by the
 * shell terminal and git panels so the root chooser sits in the header rather
 * than inside the panel body. Returns null until the slot mounts, so it
 * survives the same first-frame race PaneHeaderStatus guards against.
 */
export function PaneRootSelect({ leafId, roots, value, onChange, testId, title }: PaneRootSelectProps) {
  const { colors } = useTheme();
  const [slot, setSlot] = useState<HTMLElement | null>(null);

  useEffect(() => {
    const find = () => document.getElementById(`pane-header-slot-${leafId}`);
    setSlot(find());
    if (find()) return;
    // Slot may mount in the same frame; retry once after paint.
    const raf = requestAnimationFrame(() => setSlot(find()));
    return () => cancelAnimationFrame(raf);
  }, [leafId]);

  if (!slot) return null;

  return createPortal(
    <span style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
      <span style={{ fontSize: 10, color: colors.textDim }}>root</span>
      <select
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        title={title}
        data-testid={testId}
        style={{
          background: colors.surface,
          color: colors.textLight,
          border: `1px solid ${colors.border}`,
          borderRadius: 4,
          fontSize: 10,
          fontFamily: fonts.mono,
          padding: "0 2px",
          outline: "none",
          maxWidth: 160,
          cursor: "pointer",
        }}
      >
        {roots.map((r) => (
          <option key={r.index} value={r.index} title={r.path}>
            {r.path}
          </option>
        ))}
      </select>
    </span>,
    slot,
  );
}
