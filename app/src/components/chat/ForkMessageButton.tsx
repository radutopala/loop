import { useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { Spinner } from "./ExplainButton";

/**
 * "+fork" on a chat bubble: forks the conversation at the message into a new
 * thread. Shown while its bubble is hovered, and while a fork runs or after
 * one failed (red, the error in its title; a click tries again).
 */
export function ForkMessageButton({ onFork, visible, style }: { onFork: () => Promise<void>; visible: boolean; style?: React.CSSProperties }) {
  const { colors } = useTheme();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const color = error ? colors.error : colors.textDim;
  const onClick = () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    onFork()
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };

  return (
    <button
      type="button"
      data-testid="fork-message-btn"
      data-state={busy ? "pending" : error ? "error" : ""}
      onClick={onClick}
      title={error ? `Forking failed: ${error}\nClick to try again.` : "Fork the conversation at this message into a new thread"}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        padding: "0 8px",
        height: 20,
        marginTop: 6,
        background: "transparent",
        border: `1px solid ${color}`,
        borderRadius: 8,
        color,
        cursor: busy ? "default" : "pointer",
        fontFamily: fonts.mono,
        fontSize: 11,
        lineHeight: 1,
        opacity: visible || busy || error ? 1 : 0,
        transition: "opacity 0.12s ease",
        ...style,
      }}
      onMouseEnter={(e) => {
        if (!error) e.currentTarget.style.color = colors.textLight;
      }}
      onMouseLeave={(e) => {
        if (!error) e.currentTarget.style.color = colors.textDim;
      }}
    >
      {busy && <Spinner />}
      {error ? "Fork failed" : "+fork"}
    </button>
  );
}
