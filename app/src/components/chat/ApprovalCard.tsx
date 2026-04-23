import { useState } from "react";
import type { GateApprovalRequestedData } from "../../types";
import { resolveGateApproval } from "../../api/loopApi";
import type { GateDecision } from "../../api/gate";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";

export function ApprovalCard({ data, onResolved, style }: {
  data: GateApprovalRequestedData;
  onResolved?: () => void;
  style?: React.CSSProperties;
}) {
  const { colors } = useTheme();
  const [sending, setSending] = useState<GateDecision | null>(null);
  const [error, setError] = useState<string | null>(null);

  const resolve = async (decision: GateDecision) => {
    setSending(decision);
    setError(null);
    try {
      await resolveGateApproval(data.req_id, decision);
      onResolved?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSending(null);
    }
  };

  const label = data.kind ? data.kind.toUpperCase() : "APPROVAL";

  return (
    <div style={{
      margin: "8px 16px",
      padding: "12px 16px",
      borderRadius: 8,
      border: `1px solid ${colors.warning}`,
      backgroundColor: colors.surface,
      ...style,
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: colors.warning, textTransform: "uppercase", letterSpacing: 1, marginBottom: 8 }}>
        Gate · {label}
      </div>
      <div style={{ fontSize: 13, color: colors.text, marginBottom: 4, fontFamily: fonts.mono, wordBreak: "break-word" }}>
        {data.target}
      </div>
      {data.message && (
        <div style={{ fontSize: 12, color: colors.textDim, marginBottom: 10 }}>
          {data.message}
        </div>
      )}
      {data.details && Object.keys(data.details).length > 0 && (
        <div style={{
          fontSize: 12,
          fontFamily: fonts.mono,
          marginBottom: 10,
          padding: "6px 10px",
          borderRadius: 6,
          backgroundColor: colors.codeBlockBg,
          color: colors.textDim,
        }}>
          {Object.keys(data.details).sort().map((k) => (
            <div key={k} style={{ wordBreak: "break-word" }}>
              <span style={{ color: colors.text }}>{k}</span>: {data.details![k]}
            </div>
          ))}
        </div>
      )}
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <ApprovalButton label="Allow once"        decision="once"    busy={sending === "once"}    disabled={sending !== null} onClick={resolve} variant="primary"   />
        <ApprovalButton label="Allow for session" decision="session" busy={sending === "session"} disabled={sending !== null} onClick={resolve} variant="secondary" />
        <ApprovalButton label="Deny"              decision="deny"    busy={sending === "deny"}    disabled={sending !== null} onClick={resolve} variant="danger"    />
      </div>
      {error && (
        <div style={{ marginTop: 8, fontSize: 11, color: colors.warning, fontFamily: fonts.mono }}>
          {error}
        </div>
      )}
    </div>
  );
}

function ApprovalButton({ label, decision, busy, disabled, onClick, variant }: {
  label: string;
  decision: GateDecision;
  busy: boolean;
  disabled: boolean;
  onClick: (d: GateDecision) => void;
  variant: "primary" | "secondary" | "danger";
}) {
  const { colors } = useTheme();
  const accent = variant === "primary" ? colors.active : variant === "danger" ? colors.warning : colors.border;
  const textColor = variant === "secondary" ? colors.text : "#fff";
  const bg = variant === "secondary" ? "transparent" : accent;
  return (
    <button
      onClick={() => onClick(decision)}
      disabled={disabled}
      style={{
        padding: "4px 12px",
        fontSize: 12,
        fontFamily: fonts.mono,
        border: `1px solid ${accent}`,
        borderRadius: 12,
        backgroundColor: bg,
        color: textColor,
        cursor: disabled ? "default" : "pointer",
        opacity: disabled && !busy ? 0.5 : busy ? 0.7 : 1,
      }}
    >
      {busy ? "..." : label}
    </button>
  );
}
