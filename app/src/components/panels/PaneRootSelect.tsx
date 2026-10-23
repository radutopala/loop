import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RootEntry } from "../../api/files";
import { useTheme } from "../../ThemeContext";
import { RootPicker } from "./RootPicker";

interface PaneRootSelectProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  roots: RootEntry[];
  value: number;
  onChange: (index: number) => void;
  /** Stable test id for the picker button. */
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
      <RootPicker roots={roots} value={value} onChange={onChange} testId={testId} title={title} />
    </span>,
    slot,
  );
}
