import { useState } from "react";
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

interface StatusBadgeProps {
  status: SessionStatus;
  elapsed?: number;
}

function formatElapsed(s: number): string {
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
}

export function StatusBadge({ status, elapsed }: StatusBadgeProps) {
  const { colors } = useTheme();
  const [hovered, setHovered] = useState(false);
  const showElapsed = hovered && elapsed != null && elapsed > 0;
  return (
    <span
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        position: "relative",
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        padding: "2px 10px",
        borderRadius: 12,
        fontSize: 12,
        fontWeight: 600,
        color: colors.white,
        backgroundColor: statusColor(status, colors),
        textTransform: "capitalize",
      }}
    >
      <span
        style={{
          width: 6,
          height: 6,
          borderRadius: "50%",
          backgroundColor: colors.white,
          opacity: status === "running" ? 1 : 0.6,
        }}
      />
      {status}
      {showElapsed && (
        <span
          style={{
            position: "absolute",
            left: "50%",
            top: "calc(100% + 6px)",
            transform: "translateX(-50%)",
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
            textTransform: "none",
          }}
        >
          {formatElapsed(elapsed!)}
        </span>
      )}
    </span>
  );
}
