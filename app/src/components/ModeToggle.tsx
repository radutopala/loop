import type { ViewMode } from "../types";
import { colors } from "../theme";

interface ModeToggleProps {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
}

const modes: { value: ViewMode; label: string; hint: string }[] = [
  { value: "chat", label: "Chat", hint: "headless" },
  { value: "split", label: "Split", hint: "both" },
  { value: "terminal", label: "Terminal", hint: "interactive" },
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
          title={m.hint}
          style={{
            padding: "2px 8px",
            fontSize: 11,
            border: mode === m.value ? "1px solid rgba(255,255,255,0.3)" : "1px solid transparent",
            borderRadius: 4,
            cursor: "pointer",
            backgroundColor:
              mode === m.value ? colors.selectedBg : "transparent",
            color: mode === m.value ? colors.textLight : colors.textMuted,
            fontWeight: mode === m.value ? 600 : 400,
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            gap: 0,
          }}
        >
          <span>{m.label}</span>
          <span style={{ fontSize: 8, fontWeight: 400, opacity: 0.6 }}>{m.hint}</span>
        </button>
      ))}
    </div>
  );
}
