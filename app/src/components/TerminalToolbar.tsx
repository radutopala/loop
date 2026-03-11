import type { SessionStatus } from "../types";
import { colors } from "../theme";
import { StatusBadge } from "./StatusBadge";
import { ElapsedTimer } from "./ElapsedTimer";

interface TerminalToolbarProps {
  status: SessionStatus;
  elapsed: number;
  onKill: () => void;
}

export function TerminalToolbar({ status, elapsed, onKill }: TerminalToolbarProps) {
  const isRunning = status === "running";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "8px 16px",
        borderBottom: `1px solid ${colors.border}`,
        backgroundColor: colors.surface,
      }}
    >
      <StatusBadge status={status} />
      <button
        onClick={onKill}
        disabled={!isRunning}
        title="Kill session and remove container"
        style={{
          padding: "4px 12px",
          borderRadius: 6,
          border: `1px solid ${colors.error}`,
          backgroundColor: "transparent",
          color: isRunning ? colors.error : colors.textDisabled,
          cursor: isRunning ? "pointer" : "default",
          fontSize: 12,
        }}
      >
        Kill
      </button>
      <ElapsedTimer seconds={elapsed} />
    </div>
  );
}
