import { useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";

/** The backend's cap, in characters. */
export const MAX_DESCRIPTION_LENGTH = 500;

interface DescriptionDialogProps {
  /** Current description, used to prefill the input. */
  currentDescription: string;
  /** The thread's name, shown in the title. */
  name: string;
  onCancel: () => void;
  /** Called with the trimmed description; empty clears it. */
  onSubmit: (description: string) => void;
}

/**
 * Modal prompt for a thread's description, shown when hovering it in the
 * sidebar. Enter saves, Shift+Enter starts a new line; saving it empty clears it.
 */
export function DescriptionDialog({ currentDescription, name, onCancel, onSubmit }: DescriptionDialogProps) {
  const { colors } = useTheme();
  const [description, setDescription] = useState(currentDescription);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const trimmed = description.trim();
  const canSubmit = trimmed !== currentDescription.trim();
  const submit = () => {
    if (canSubmit) onSubmit(trimmed);
  };

  return (
    <div
      data-testid="description-dialog"
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onCancel}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 480, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 12, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Description of {name}</div>
        <textarea
          ref={inputRef}
          data-testid="description-input"
          value={description}
          maxLength={MAX_DESCRIPTION_LENGTH}
          rows={3}
          placeholder="What is this thread for?"
          onChange={(e) => setDescription(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
            if (e.key === "Escape") {
              e.preventDefault();
              onCancel();
            }
          }}
          style={{
            padding: "8px 10px",
            borderRadius: 6,
            border: `1px solid ${colors.border}`,
            background: colors.bg,
            color: colors.text,
            fontSize: 14,
            fontFamily: "inherit",
            outline: "none",
            width: "100%",
            boxSizing: "border-box",
            resize: "vertical",
          }}
        />
        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 12 }}>
          <span style={{ flex: 1, fontSize: 11, color: colors.textDisabled }}>
            {description.length}/{MAX_DESCRIPTION_LENGTH}
          </span>
          <button
            onClick={onCancel}
            style={{ padding: "6px 14px", borderRadius: 6, border: `1px solid ${colors.border}`, background: "transparent", color: colors.textDim, fontSize: 13, cursor: "pointer" }}
          >
            Cancel
          </button>
          <button
            data-testid="description-submit"
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
            Save
          </button>
        </div>
      </div>
    </div>
  );
}
