import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { SessionStatus } from "../../types";

function statusColor(status: SessionStatus, colors: ColorPalette): string {
  switch (status) {
    case "connecting":
      return colors.warning;
    case "running":
      return colors.active;
    case "completed":
      return colors.textDim;
    case "failed":
      return colors.error;
  }
}

function statusLabel(status: SessionStatus): string {
  switch (status) {
    case "connecting":
      return "Connecting";
    case "running":
      return "Running";
    case "completed":
      return "Completed";
    case "failed":
      return "Failed";
  }
}

function formatElapsed(s: number): string {
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
}

interface PaneHeaderStatusProps {
  leafId: string;
  status: SessionStatus;
  elapsed: number;
}

export function PaneHeaderStatus({ leafId, status, elapsed }: PaneHeaderStatusProps) {
  const { colors } = useTheme();
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  const [hovered, setHovered] = useState(false);

  useEffect(() => {
    const find = () => document.getElementById(`pane-header-slot-${leafId}`);
    setSlot(find());
    if (find()) return;
    // Slot may mount in the same frame; retry once after paint.
    const raf = requestAnimationFrame(() => setSlot(find()));
    return () => cancelAnimationFrame(raf);
  }, [leafId]);

  if (!slot) return null;

  const showElapsed = hovered && elapsed > 0;
  const dotColor = statusColor(status, colors);
  const label = statusLabel(status);

  return createPortal(
    <span
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        fontSize: 10,
        color: colors.textDim,
        padding: "0 6px",
        lineHeight: 1,
        position: "relative",
      }}
    >
      <span
        style={{
          width: 6,
          height: 6,
          borderRadius: "50%",
          backgroundColor: dotColor,
          opacity: status === "running" ? 1 : 0.75,
          flexShrink: 0,
        }}
      />
      <span>{label}</span>
      {showElapsed && (
        <span
          style={{
            position: "absolute",
            right: 0,
            top: "calc(100% + 4px)",
            padding: "3px 8px",
            borderRadius: 4,
            fontSize: 11,
            fontFamily: fonts.mono,
            fontWeight: 400,
            color: colors.text,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            whiteSpace: "nowrap",
            pointerEvents: "none",
            zIndex: 1000,
          }}
        >
          {formatElapsed(elapsed)}
        </span>
      )}
    </span>,
    slot,
  );
}
