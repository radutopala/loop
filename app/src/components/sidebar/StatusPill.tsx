import { fonts } from "../../theme";

interface StatusPillProps {
  label: string;
  color: string;
  title: string;
  marginLeft?: number | string;
}

export function StatusPill({ label, color, title, marginLeft }: StatusPillProps) {
  return (
    <span
      title={title}
      style={{
        flexShrink: 0,
        fontSize: 9,
        fontFamily: fonts.mono,
        lineHeight: 1,
        padding: "2px 4px",
        borderRadius: 3,
        color,
        border: `1px solid ${color}`,
        marginLeft,
      }}
    >
      {label}
    </span>
  );
}
