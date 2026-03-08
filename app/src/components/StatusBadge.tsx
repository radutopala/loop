import type { SessionStatus } from "../types";
import { colors } from "../theme";

const statusColors: Record<SessionStatus, string> = {
  connecting: colors.warning,
  running: colors.active,
  completed: colors.textDim,
  failed: colors.error,
};

interface StatusBadgeProps {
  status: SessionStatus;
}

export function StatusBadge({ status }: StatusBadgeProps) {
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
        backgroundColor: statusColors[status],
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
