import { useEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

// formatCountdown renders whole seconds remaining as m:ss, or h:mm:ss once the
// delay is an hour or more. Negative inputs clamp to 0:00.
export function formatCountdown(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const hrs = Math.floor(s / 3600);
  const mins = Math.floor((s % 3600) / 60);
  const secs = s % 60;
  const mm = String(mins).padStart(2, "0");
  const ss = String(secs).padStart(2, "0");
  if (hrs > 0) return `${hrs}:${mm}:${ss}`;
  return `${mins}:${ss}`;
}

interface DelayCountdownProps {
  // Unix-seconds timestamp the delayed message becomes eligible to run.
  notBefore: number;
}

// DelayCountdown shows a live-ticking chip counting down to a delayed message's
// scheduled run time (queue_message with a delay). It re-renders once per second
// and renders nothing once the delay has elapsed — at which point the drain
// picks the message up on its own.
export function DelayCountdown({ notBefore }: DelayCountdownProps) {
  const { colors } = useTheme();
  const [remaining, setRemaining] = useState(() => notBefore - Date.now() / 1000);

  useEffect(() => {
    setRemaining(notBefore - Date.now() / 1000);
    const id = setInterval(() => {
      setRemaining(notBefore - Date.now() / 1000);
    }, 1000);
    return () => clearInterval(id);
  }, [notBefore]);

  if (remaining <= 0) return null;

  return (
    <span
      title="Scheduled — runs when the countdown ends"
      style={{
        flexShrink: 0,
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        padding: "1px 6px",
        borderRadius: 10,
        fontFamily: fonts.mono,
        fontSize: 11,
        color: colors.textMuted,
        border: `1px solid ${colors.border}`,
      }}
    >
      {"⏱"} {formatCountdown(remaining)}
    </span>
  );
}
