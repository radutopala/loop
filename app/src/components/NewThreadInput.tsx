import { useState } from "react";
import { colors } from "../theme";

interface NewThreadInputProps {
  onSubmit: (name: string) => void;
  onCancel: () => void;
}

export function NewThreadInput({ onSubmit, onCancel }: NewThreadInputProps) {
  const [name, setName] = useState("");

  return (
    <div style={{ padding: "4px 12px 4px 32px" }}>
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
          width: "100%",
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
    </div>
  );
}
