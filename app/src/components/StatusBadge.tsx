import type { SessionStatus } from "../types";

const statusColors: Record<SessionStatus, string> = {
  connecting: "#f59e0b",
  running: "#22c55e",
  completed: "#6b7280",
  failed: "#ef4444",
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
        color: "#fff",
        backgroundColor: statusColors[status],
        textTransform: "capitalize",
      }}
    >
      <span
        style={{
          width: 6,
          height: 6,
          borderRadius: "50%",
          backgroundColor: "#fff",
          opacity: status === "running" ? 1 : 0.6,
        }}
      />
      {status}
    </span>
  );
}
