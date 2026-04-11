import type { ColorPalette } from "../theme";

export function timeAgo(dateStr: string | null): string {
  if (!dateStr) return "-";
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "-";
  const diff = Date.now() - d.getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function elapsed(start: string | null, end: string | null): string {
  if (!start) return "-";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const secs = Math.max(0, Math.floor((e - s) / 1000));
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ${secs % 60}s`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

export const STATUS_COLORS: Record<string, string> = {
  running: "#818cf8",
  completed: "#34d399",
  failed: "#ef4444",
  paused: "#fbbf24",
  cancelled: "#6b7280",
};

export function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

export function buildBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    padding: "4px 10px",
    background: colors.active,
    border: "none",
    borderRadius: 4,
    color: "#fff",
    fontSize: 12,
    cursor: "pointer",
  };
}

export function buildBtnSecondaryStyle(colors: ColorPalette): React.CSSProperties {
  return {
    ...buildBtnStyle(colors),
    background: "transparent",
    border: `1px solid ${colors.border}`,
    color: colors.text,
  };
}

export function buildInputStyle(colors: ColorPalette): React.CSSProperties {
  return {
    width: "100%",
    padding: "4px 8px",
    background: colors.surface,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.text,
    fontSize: 12,
    outline: "none",
    boxSizing: "border-box",
  };
}

export const hoverIn = (e: React.MouseEvent<HTMLButtonElement>, colors: ColorPalette) => {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
};

export const hoverOut = (e: React.MouseEvent<HTMLButtonElement>, colors: ColorPalette) => {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
};
