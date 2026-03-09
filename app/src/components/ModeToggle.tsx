import type { ViewMode } from "../types";
import { colors } from "../theme";

interface ModeToggleProps {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
}

const modes: { value: ViewMode; label: string }[] = [
  { value: "chat", label: "Chat" },
  { value: "terminal", label: "Terminal" },
];

export function ModeToggle({ mode, onChange }: ModeToggleProps) {
  return (
    <div
      style={{
        display: "flex",
        gap: 2,
        padding: 2,
        backgroundColor: colors.surface,
        borderRadius: 6,
      }}
    >
      {modes.map((m) => (
        <button
          key={m.value}
          onClick={() => onChange(m.value)}
          style={{
            padding: "3px 10px",
            fontSize: 12,
            border: "none",
            borderRadius: 4,
            cursor: "pointer",
            backgroundColor:
              mode === m.value ? colors.selectedBg : "transparent",
            color: mode === m.value ? colors.textLight : colors.textMuted,
            fontWeight: mode === m.value ? 600 : 400,
          }}
        >
          {m.label}
        </button>
      ))}
    </div>
  );
}
