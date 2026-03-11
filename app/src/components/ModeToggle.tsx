import type { ViewMode } from "../types";
import { colors } from "../theme";

interface ModeToggleProps {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
}

const modes: { value: ViewMode; label: string; hint: string }[] = [
  { value: "chat", label: "Chat", hint: "headless" },
  { value: "terminal", label: "Terminal", hint: "interactive" },
];

export function ModeToggle({ mode, onChange }: ModeToggleProps) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <span
        style={{
          fontSize: 10,
          fontWeight: 700,
          color: colors.textDim,
          textTransform: "uppercase",
          letterSpacing: 1,
        }}
      >
        Agent
      </span>
      <div
        style={{
          display: "flex",
          gap: 2,
          padding: 2,
          backgroundColor: colors.surface,
          borderRadius: 6,
        }}
      >
        {modes.map((m) => {
          const isActive = mode === m.value;
          return (
            <button
              key={m.value}
              onClick={() => onChange(m.value)}
              title={m.hint}
              style={{
                padding: "2px 8px",
                fontSize: 11,
                border: isActive ? "1px solid rgba(255,255,255,0.3)" : "1px solid transparent",
                borderRadius: 4,
                cursor: "pointer",
                backgroundColor: isActive ? colors.selectedBg : "transparent",
                color: isActive ? colors.textLight : colors.textMuted,
                fontWeight: isActive ? 600 : 400,
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                gap: 0,
              }}
            >
              <span>{m.label}</span>
              <span style={{ fontSize: 8, fontWeight: 400, opacity: 0.6 }}>{m.hint}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
