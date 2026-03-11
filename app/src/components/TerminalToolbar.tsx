import type { SessionStatus } from "../types";
import { colors } from "../theme";
import { StatusBadge } from "./StatusBadge";
import { ElapsedTimer } from "./ElapsedTimer";

interface TerminalToolbarProps {
  status: SessionStatus;
  elapsed: number;
  onKill: () => void;
  onRestart?: () => void;
  killLabel?: string;
  killTitle?: string;
}

export function TerminalToolbar({ status, elapsed, onKill, onRestart, killLabel = "Kill", killTitle = "Kill session and remove container" }: TerminalToolbarProps) {
  const isRunning = status === "running";
  const isDead = status === "completed" || status === "failed";

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
      {isDead && onRestart ? (
        <button
          onClick={onRestart}
          title="Start a new session"
          style={{
            padding: "4px 12px",
            borderRadius: 6,
            border: `1px solid ${colors.active}`,
            backgroundColor: "transparent",
            color: colors.active,
            cursor: "pointer",
            fontSize: 12,
          }}
        >
          Restart
        </button>
      ) : (
        <button
          onClick={onKill}
          disabled={!isRunning}
          title={killTitle}
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
          {killLabel}
        </button>
      )}
      <ElapsedTimer seconds={elapsed} />
    </div>
  );
}
