import type { SessionStatus } from "../types";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";

function statusColor(status: SessionStatus, colors: ColorPalette): string {
  switch (status) {
    case "connecting": return colors.warning;
    case "running": return colors.active;
    case "completed": return colors.textDim;
    case "failed": return colors.error;
  }
}

interface StatusBadgeProps {
  status: SessionStatus;
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const { colors } = useTheme();
  return (
    <span
      style={{
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
    </span>
  );
}
