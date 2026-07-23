import { useTheme } from "../../ThemeContext";

interface ElapsedTimerProps {
  seconds: number;
}

function formatElapsed(s: number): string {
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
}

export function ElapsedTimer({ seconds }: ElapsedTimerProps) {
  const { colors } = useTheme();
  return <span style={{ color: colors.textMuted, fontSize: 12, fontFamily: "monospace" }}>{formatElapsed(seconds)}</span>;
}
