import { useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";

interface RenameThreadDialogProps {
  /** Current display name, used to prefill the input. */
  currentName: string;
  /** Only changes the dialog title; renaming never touches the worktree dir or branch. */
  isWorktree: boolean;
  onCancel: () => void;
  onSubmit: (newName: string) => void;
}

/** Modal prompt for renaming a thread or worktree thread from the sidebar. */
export function RenameThreadDialog({ currentName, isWorktree, onCancel, onSubmit }: RenameThreadDialogProps) {
  const { colors } = useTheme();
  const [name, setName] = useState(currentName);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const trimmed = name.trim();
  const canSubmit = trimmed !== "" && trimmed !== currentName;
  const submit = () => {
    if (canSubmit) onSubmit(trimmed);
  };

  const inputStyle: React.CSSProperties = {
    padding: "8px 10px",
    borderRadius: 6,
    border: `1px solid ${colors.border}`,
    background: colors.bg,
    color: colors.text,
    fontSize: 14,
    outline: "none",
    width: "100%",
    boxSizing: "border-box",
  };

  return (
    <div
      data-testid="rename-thread-dialog"
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onCancel}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 420, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 12 }}>{isWorktree ? "Rename Worktree" : "Rename Thread"}</div>
        <input
          ref={inputRef}
          data-testid="rename-thread-input"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              submit();
            }
            if (e.key === "Escape") {
              e.preventDefault();
              onCancel();
            }
          }}
          style={inputStyle}
        />
        <div style={{ display: "flex", gap: 6, justifyContent: "flex-end", marginTop: 12 }}>
          <button
            onClick={onCancel}
            style={{ padding: "6px 14px", borderRadius: 6, border: `1px solid ${colors.border}`, background: "transparent", color: colors.textDim, fontSize: 13, cursor: "pointer" }}
          >
            Cancel
          </button>
          <button
            data-testid="rename-thread-submit"
            onClick={submit}
            disabled={!canSubmit}
            style={{
              padding: "6px 14px",
              borderRadius: 6,
              border: "none",
              background: canSubmit ? colors.active : colors.border,
              color: colors.white,
              fontSize: 13,
              cursor: canSubmit ? "pointer" : "default",
              opacity: canSubmit ? 1 : 0.6,
            }}
          >
            Rename
          </button>
        </div>
      </div>
    </div>
  );
}
