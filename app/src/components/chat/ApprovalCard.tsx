import { useState } from "react";
import type { GateApprovalRequestedData } from "../../types";
import { resolveGateApproval, sendMessage } from "../../api/loopApi";
import type { GateDecision } from "../../api/gate";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";

export function ApprovalCard({ data, channelId, onResolved, onDenyWithPrompt, style }: {
  data: GateApprovalRequestedData;
  channelId: string;
  onResolved?: () => void;
  /** When provided, "Deny with prompt" routes the typed text here instead of
   * posting it as a chat message. Terminal panes use this to inject the prompt
   * into the originating pane's stdin so the in-pane agent (e.g. Claude Code
   * TUI) receives it, rather than the chat agent. */
  onDenyWithPrompt?: (text: string) => void | Promise<void>;
  style?: React.CSSProperties;
}) {
  const { colors } = useTheme();
  const [sending, setSending] = useState<GateDecision | "deny-with-prompt" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showPrompt, setShowPrompt] = useState(false);
  const [prompt, setPrompt] = useState("");

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

  const denyWithPrompt = async () => {
    const text = prompt.trim();
    if (!text) return;
    setSending("deny-with-prompt");
    setError(null);
    try {
      await resolveGateApproval(data.req_id, "deny");
      if (onDenyWithPrompt) {
        await onDenyWithPrompt(text);
      } else {
        // Cancel the now-resumed run and queue the prompt as the latest user
        // message; the next run will see the deny tool result plus this new
        // instruction at the top of the agent's context.
        await sendMessage(channelId, text, undefined, true);
      }
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
        <button
          onClick={() => { setShowPrompt((s) => !s); setError(null); }}
          disabled={sending !== null}
          style={{
            padding: "4px 12px",
            fontSize: 12,
            fontFamily: fonts.mono,
            border: `1px solid ${colors.warning}`,
            borderRadius: 12,
            backgroundColor: "transparent",
            color: colors.warning,
            cursor: sending !== null ? "default" : "pointer",
            opacity: sending !== null ? 0.5 : 1,
          }}
        >
          Deny with prompt…
        </button>
      </div>
      {showPrompt && (
        <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 6 }}>
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void denyWithPrompt();
              }
            }}
            placeholder="Tell the agent what to do instead (Enter to send, Shift+Enter for newline)…"
            rows={3}
            disabled={sending !== null}
            style={{
              fontFamily: fonts.mono,
              fontSize: 12,
              padding: 8,
              borderRadius: 6,
              border: `1px solid ${colors.border}`,
              backgroundColor: colors.codeBlockBg,
              color: colors.text,
              resize: "vertical",
            }}
          />
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button
              onClick={() => { setShowPrompt(false); setPrompt(""); }}
              disabled={sending !== null}
              style={{
                padding: "4px 12px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.border}`,
                borderRadius: 12,
                backgroundColor: "transparent",
                color: colors.textDim,
                cursor: sending !== null ? "default" : "pointer",
                opacity: sending !== null ? 0.5 : 1,
              }}
            >
              Cancel
            </button>
            <button
              onClick={() => void denyWithPrompt()}
              disabled={sending !== null || prompt.trim().length === 0}
              style={{
                padding: "4px 12px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.warning}`,
                borderRadius: 12,
                backgroundColor: colors.warning,
                color: "#fff",
                cursor: (sending !== null || prompt.trim().length === 0) ? "default" : "pointer",
                opacity: (sending !== null || prompt.trim().length === 0) ? 0.5 : sending === "deny-with-prompt" ? 0.7 : 1,
              }}
            >
              {sending === "deny-with-prompt" ? "..." : "Deny & send prompt"}
            </button>
          </div>
        </div>
      )}
      {error && (
        <div style={{ marginTop: 8, display: "flex", alignItems: "flex-start", gap: 8, justifyContent: "space-between" }}>
          <div style={{ fontSize: 11, color: colors.warning, fontFamily: fonts.mono, flex: 1, wordBreak: "break-word" }}>
            {error}
          </div>
          <button
            onClick={() => onResolved?.()}
            style={{
              padding: "2px 10px",
              fontSize: 11,
              fontFamily: fonts.mono,
              border: `1px solid ${colors.border}`,
              borderRadius: 12,
              backgroundColor: "transparent",
              color: colors.textDim,
              cursor: "pointer",
              flexShrink: 0,
            }}
          >
            Dismiss
          </button>
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
