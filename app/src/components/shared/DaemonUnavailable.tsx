/** Full-window notice shown when the app can't load from the Loop daemon at startup. */
import { useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { LoopLogo } from "./LoopLogo";

export function DaemonUnavailable({ apiUrl, error, running, onRetry }: { apiUrl: string; error: string; running: boolean; onRetry: () => Promise<void> }) {
  const { colors } = useTheme();
  const [busy, setBusy] = useState(false);
  // The desktop app can (re)start the daemon itself; a plain browser can only retry.
  const canStart = Boolean(window.loopAPI?.restartDaemon);

  const handleClick = async () => {
    setBusy(true);
    try {
      if (canStart) await window.loopAPI.restartDaemon();
      await onRetry();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        height: "100vh",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 16,
        padding: 24,
        backgroundColor: colors.bg,
        color: colors.text,
        fontFamily: fonts.sans,
        textAlign: "center",
      }}
    >
      <LoopLogo />
      <div style={{ fontSize: 16, fontWeight: 600, color: colors.textLight }}>{running ? "Couldn't load from the Loop daemon" : "Loop daemon isn't running"}</div>
      <div style={{ maxWidth: 440, fontSize: 13, lineHeight: 1.5, color: colors.textMuted }}>
        The app talks to a local daemon (<code style={{ fontFamily: fonts.mono }}>loop serve</code>) at <code style={{ fontFamily: fonts.mono }}>{apiUrl}</code>,{" "}
        {running ? "which is running but rejected the request" : "which could not be reached"}. Details are in <code style={{ fontFamily: fonts.mono }}>~/.loop/loop.log</code>.
      </div>
      {error && <div style={{ maxWidth: 440, fontSize: 12, fontFamily: fonts.mono, color: colors.textDim, wordBreak: "break-word" }}>{error}</div>}
      <button
        onClick={handleClick}
        disabled={busy}
        style={{
          padding: "8px 16px",
          backgroundColor: colors.surface,
          border: `1px solid ${colors.border}`,
          borderRadius: 8,
          color: busy ? colors.textDim : colors.text,
          fontSize: 12,
          cursor: busy ? "default" : "pointer",
          fontFamily: "inherit",
        }}
      >
        {busy ? (canStart ? "Starting…" : "Retrying…") : canStart ? (running ? "Restart daemon" : "Start daemon") : "Retry"}
      </button>
    </div>
  );
}
