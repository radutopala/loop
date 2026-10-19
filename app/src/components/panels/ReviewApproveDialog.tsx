import { useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";

interface ReviewApproveDialogProps {
  prNumber?: number;
  /** The head the approval goes on. */
  headSha?: string;
  /** The approval's text to start from (see approveBody). */
  initialBody: string;
  /** Approving past an outcome that doesn't allow it. */
  force: boolean;
  /** Why the outcome doesn't allow it, shown when force is set. */
  warning?: string;
  onCancel: () => void;
  /** Approves with the text as edited; a rejection's message is shown in the dialog. */
  onSubmit: (body: string) => Promise<void>;
}

/**
 * Confirms approving the PR on GitHub, with the review's text editable
 * first. Cmd/Ctrl+Enter approves, Escape cancels.
 */
export function ReviewApproveDialog({ prNumber, headSha, initialBody, force, warning, onCancel, onSubmit }: ReviewApproveDialogProps) {
  const { colors } = useTheme();
  const [body, setBody] = useState(initialBody);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await onSubmit(body.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <div
      data-testid="review-approve-dialog"
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={() => {
        if (!busy) onCancel();
      }}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 560, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 4 }}>
          {force ? "Approve anyway" : "Approve"} #{prNumber} on GitHub
        </div>
        <div style={{ fontSize: 11, color: colors.textDim, marginBottom: 12 }}>
          At <span style={{ fontFamily: "monospace" }}>{headSha?.slice(0, 7)}</span>. The text below is posted as the approval's comment; leave it empty for none.
        </div>
        {force && warning && (
          <div data-testid="review-approve-warning" style={{ fontSize: 11, color: colors.dangerText, marginBottom: 8 }}>
            {warning}
          </div>
        )}
        <textarea
          ref={inputRef}
          data-testid="review-approve-body"
          value={body}
          rows={8}
          placeholder="Optional comment"
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void submit();
            }
            if (e.key === "Escape" && !busy) {
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
            fontSize: 12,
            fontFamily: "inherit",
            outline: "none",
            width: "100%",
            boxSizing: "border-box",
            resize: "vertical",
          }}
        />
        {error && (
          <div data-testid="review-approve-error" style={{ marginTop: 8, padding: "4px 8px", borderRadius: 4, background: colors.dangerBg, color: colors.dangerText, fontSize: 11 }}>
            {error}
          </div>
        )}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
          <button
            onClick={onCancel}
            disabled={busy}
            style={{ padding: "6px 14px", borderRadius: 6, border: `1px solid ${colors.border}`, background: "transparent", color: colors.textDim, fontSize: 13, cursor: busy ? "default" : "pointer" }}
          >
            Cancel
          </button>
          <button
            data-testid="review-approve-submit"
            onClick={() => void submit()}
            disabled={busy}
            style={{
              padding: "6px 14px",
              borderRadius: 6,
              border: "none",
              background: force ? colors.dangerText : colors.active,
              color: colors.white,
              fontSize: 13,
              cursor: busy ? "default" : "pointer",
              opacity: busy ? 0.6 : 1,
            }}
          >
            {busy ? "Approving..." : force ? "Approve anyway" : "Approve"}
          </button>
        </div>
      </div>
    </div>
  );
}
