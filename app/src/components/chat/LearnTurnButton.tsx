import { useContext } from "react";
import { LearnContext } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { Spinner } from "./ExplainButton";
import { LearnIcon } from "./LearnIcon";
import { isOpenProposal, learnTurnLabel } from "./learnState";

/**
 * The turn's Learn action, at the end of the turn beside its Explain one.
 * A turn without a learn pass (Learn off, or, on an old pass, superseded
 * by the next turn's before it ran) offers one: a click learns from it, whatever
 * the Learn switch says. Otherwise it follows the pass: queued, running,
 * failed (a click tries again), or how many proposals it filed, lit while
 * any still waits on the user. A click shows the turn's proposals in the
 * Learn view.
 */
export function LearnTurnButton({ messageId }: { messageId: string }) {
  const { colors } = useTheme();
  const view = useContext(LearnContext);
  if (!view || !view.loaded || !view.available) return null;
  const pass = view.passByMessage.get(messageId);
  const status = pass && pass.status !== "superseded" ? pass.status : undefined;
  const mine = view.proposals.filter((p) => p.message_id === messageId);
  const error = view.turnErrors.get(messageId);
  const pending = view.turnBusy.has(messageId) || status === "queued" || status === "running";
  const failed = !!error || status === "failed";
  // No pass of its own, or a failed one: a click learns from the turn.
  const learns = !pending && (failed || !status);
  const lit = pending || mine.some((p) => isOpenProposal(p));
  const color = failed ? colors.error : lit ? colors.active : colors.textDim;
  const label = error ? "Learn failed" : pending && !status ? "Learn queued…" : learnTurnLabel(status ? pass : undefined, mine.length);
  const title = failed
    ? `The learn pass failed${error || pass?.error ? `: ${error || pass?.error}` : ""}\nClick to try again.`
    : learns
      ? "Learn from this turn: a hidden forked session reviews it and proposes Loop changes."
      : "Show this turn's learn pass in the Learn view";
  const onClick = () => {
    if (learns) void view.learnTurn(messageId);
    else view.show(messageId);
  };

  return (
    <button
      type="button"
      data-testid="learn-turn"
      data-status={error ? "error" : pending && !status ? "queued" : (status ?? "")}
      data-open={lit ? "true" : "false"}
      onClick={onClick}
      title={title}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        padding: "0 8px",
        height: 20,
        marginTop: 6,
        background: "transparent",
        border: `1px solid ${color}`,
        borderRadius: 8,
        color,
        cursor: "pointer",
        fontFamily: fonts.mono,
        fontSize: 11,
        lineHeight: 1,
      }}
    >
      {pending ? <Spinner /> : <LearnIcon size={10} />}
      {label}
    </button>
  );
}
