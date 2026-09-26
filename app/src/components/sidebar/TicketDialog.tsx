import { useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";

import { MAX_TICKET_URL_LENGTH, ticketURLError } from "../../utils/ticketUrl";

interface TicketDialogProps {
  /** Current ticket URL, used to prefill the input. */
  currentTicketURL: string;
  /** The row's name, shown in the title. */
  name: string;
  onCancel: () => void;
  /** Called with the trimmed URL; empty clears it. */
  onSubmit: (ticketURL: string) => void;
}

/**
 * Modal prompt for a channel's or thread's ticket URL, shown when hovering it
 * in the sidebar. Enter saves; saving it empty clears it. A URL the backend
 * would reject can't be saved.
 */
export function TicketDialog({ currentTicketURL, name, onCancel, onSubmit }: TicketDialogProps) {
  const { colors } = useTheme();
  const [ticketURL, setTicketURL] = useState(currentTicketURL);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const trimmed = ticketURL.trim();
  const error = ticketURLError(ticketURL);
  const canSubmit = !error && trimmed !== currentTicketURL.trim();
  const submit = () => {
    if (canSubmit) onSubmit(trimmed);
  };

  return (
    <div
      data-testid="ticket-dialog"
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onCancel}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 480, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 12, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Ticket of {name}</div>
        <input
          ref={inputRef}
          type="url"
          data-testid="ticket-input"
          value={ticketURL}
          maxLength={MAX_TICKET_URL_LENGTH}
          placeholder="https://example.atlassian.net/browse/PROJ-123"
          onChange={(e) => setTicketURL(e.target.value)}
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
          }}
        />
        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 12 }}>
          <span data-testid="ticket-error" style={{ flex: 1, fontSize: 11, color: error ? colors.error : colors.textDisabled }}>
            {error ?? "Jira, GitHub or any tracker's URL; empty clears it"}
          </span>
          <button
            onClick={onCancel}
            style={{ padding: "6px 14px", borderRadius: 6, border: `1px solid ${colors.border}`, background: "transparent", color: colors.textDim, fontSize: 13, cursor: "pointer" }}
          >
            Cancel
          </button>
          <button
            data-testid="ticket-submit"
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
