import type { SessionStatus } from "../types";
import { colors } from "../theme";
import { StatusBadge } from "./StatusBadge";
import { ElapsedTimer } from "./ElapsedTimer";

interface TerminalToolbarProps {
  status: SessionStatus;
  elapsed: number;
  detached: boolean;
  onKill: () => void;
  onDetach: () => void;
  onReattach: () => void;
}

export function TerminalToolbar({ status, elapsed, detached, onKill, onDetach, onReattach }: TerminalToolbarProps) {
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
        disabled={!isRunning && !detached}
        title="Kill session and remove container"
        style={{
          padding: "4px 12px",
          borderRadius: 6,
          border: `1px solid ${colors.error}`,
          backgroundColor: "transparent",
          color: isRunning || detached ? colors.error : colors.textDisabled,
          cursor: isRunning || detached ? "pointer" : "default",
          fontSize: 12,
        }}
      >
        Kill
      </button>
      {detached ? (
        <button
          onClick={onReattach}
          title="Reattach to running session"
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
          Reattach
        </button>
      ) : (
        <button
          onClick={onDetach}
          disabled={!isRunning}
          title="Detach from session (keeps running)"
          style={{
            padding: "4px 12px",
            borderRadius: 6,
            border: `1px solid ${colors.textDim}`,
            backgroundColor: "transparent",
            color: isRunning ? colors.textMuted : colors.textDisabled,
            cursor: isRunning ? "pointer" : "default",
            fontSize: 12,
          }}
        >
          Detach
        </button>
      )}
      <ElapsedTimer seconds={elapsed} />
    </div>
  );
}
