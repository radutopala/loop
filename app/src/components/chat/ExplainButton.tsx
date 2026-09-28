import { useContext } from "react";
import { ExplainContext } from "../../hooks/useExplain";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ExplainIcon } from "./ExplainIcon";
import { explainAction, explainActionLabel } from "./explainState";

/**
 * The Explain action on the bot bubble that ends a turn: it explains the
 * turn, or, once explained (or while being explained), shows it in the
 * Explain pane. Re-explaining is the pane's.
 */
export function ExplainButton({ messageId }: { messageId: string }) {
  const { colors } = useTheme();
  const view = useContext(ExplainContext);
  if (!view || !view.loaded || !view.available) return null;
  const e = view.byMessage.get(messageId);
  const busy = view.busy.has(messageId);
  const action = busy ? "pending" : explainAction(e);
  const error = view.errors.get(messageId);
  const failed = action === "open" && e?.status === "failed";
  const color = error || failed ? colors.error : action === "explain" ? colors.textDim : colors.active;
  const title = error
    ? `Explaining failed: ${error}\nClick to try again.`
    : action === "explain"
      ? "Explain this turn: a hidden forked session writes up what changed, the commands, decisions, risks and how to verify it."
      : "Show the explanation in the Explain pane";
  const onClick = () => {
    if (action === "explain" || error) void view.explainTurn(messageId);
    else view.show(messageId);
  };

  return (
    <button
      type="button"
      data-testid="explain-turn"
      data-state={error ? "error" : action}
      data-status={e?.status ?? ""}
      onClick={onClick}
      title={title}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        marginLeft: 6,
        padding: "0 6px",
        height: 16,
        background: "transparent",
        border: `1px solid ${color}`,
        borderRadius: 8,
        color,
        cursor: "pointer",
        fontFamily: fonts.mono,
        fontSize: 10,
        lineHeight: 1,
      }}
    >
      {action === "pending" ? <Spinner /> : <ExplainIcon size={10} />}
      {error ? "Explain failed" : explainActionLabel(action, e)}
    </button>
  );
}

function Spinner() {
  return (
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" aria-hidden="true">
      <path d="M12 3a9 9 0 1 0 9 9">
        <animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.9s" repeatCount="indefinite" />
      </path>
    </svg>
  );
}
