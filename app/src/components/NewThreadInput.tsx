import { useState } from "react";
import { colors } from "../theme";

interface NewThreadInputProps {
  onSubmit: (name: string) => void;
  onCancel: () => void;
}

export function NewThreadInput({ onSubmit, onCancel }: NewThreadInputProps) {
  const [name, setName] = useState("");

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "4px 12px 4px 32px" }}>
      <input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            const trimmed = name.trim();
            if (trimmed) onSubmit(trimmed);
          }
          if (e.key === "Escape") onCancel();
        }}
        placeholder="Thread name..."
        style={{
          flex: 1,
          minWidth: 0,
          padding: "4px 8px",
          fontSize: 12,
          backgroundColor: colors.surface,
          border: `1px solid ${colors.inputBorder}`,
          borderRadius: 4,
          color: colors.textLight,
          outline: "none",
          boxSizing: "border-box",
        }}
      />
      <button
        onClick={onCancel}
        title="Cancel"
        style={{
          background: "none",
          border: "none",
          color: colors.textDim,
          cursor: "pointer",
          padding: "2px 4px",
          fontSize: 14,
          lineHeight: 1,
          flexShrink: 0,
        }}
      >
        ✕
      </button>
    </div>
  );
}
