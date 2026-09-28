import { useEffect, useRef, useState } from "react";
import type { Explanation } from "../../api/explain";
import type { ExplainView } from "../../hooks/useExplain";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { inAppHref, messageLink } from "../../utils/messageLinks";
import { ChannelContext } from "../chat/chatShared";
import { ExplainIcon } from "../chat/ExplainIcon";
import { explanationPending, snippetLine } from "../chat/explainState";
import { MarkdownContent } from "../chat/markdown";
import { formatMessageTimestamp } from "../chat/timestamps";

// How long the card a bubble's Explain action opened stays outlined.
const FOCUS_HIGHLIGHT_MS = 4000;

/**
 * The channel's explanations, newest first: one card per explained turn,
 * with the start of its prompt and reply, a link back to the turn's last
 * message, the write-up, and Re-explain. The card a bubble's Explain action
 * asked for is brought into view and outlined.
 */
export function ExplainPanel({ channelId, explain }: { channelId: string; explain: ExplainView }) {
  const { colors } = useTheme();
  const listRef = useRef<HTMLDivElement>(null);
  const [highlighted, setHighlighted] = useState<string | null>(null);
  const { focus, explanations } = explain;
  const focusedShown = !!focus && explanations.some((e) => e.message_id === focus.messageId);

  // Once the asked-for card is in the list (a new one comes back from the
  // request), bring it into view.
  useEffect(() => {
    if (!focus || !focusedShown) return;
    const card = listRef.current?.querySelector(`[data-explain-msg="${CSS.escape(focus.messageId)}"]`);
    card?.scrollIntoView({ behavior: "smooth", block: "start" });
    setHighlighted(focus.messageId);
    const t = setTimeout(() => setHighlighted(null), FOCUS_HIGHLIGHT_MS);
    return () => clearTimeout(t);
  }, [focus, focusedShown]);

  return (
    <ChannelContext.Provider value={channelId}>
      <div data-testid="explain-panel" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden", fontFamily: fonts.sans }}>
        <style>{"@keyframes loop-explain-blink { 50% { outline-color: transparent; } }"}</style>
        <div ref={listRef} style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
          {explanations.length === 0 ? (
            <div data-testid="explain-empty" style={{ padding: 16, color: colors.textDim, fontSize: 12, lineHeight: 1.5 }}>
              No explanations yet. Click Explain on the last message of a turn, or turn explain on in the composer to explain every turn.
            </div>
          ) : (
            explanations.map((e) => (
              <ExplanationCard
                key={e.message_id}
                channelId={channelId}
                explanation={e}
                highlighted={highlighted === e.message_id}
                busy={explain.busy.has(e.message_id)}
                error={explain.errors.get(e.message_id)}
                onReexplain={() => void explain.explainTurn(e.message_id, true)}
              />
            ))
          )}
          {focus && !focusedShown && explain.errors.has(focus.messageId) && (
            <div data-testid="explain-request-error" style={{ padding: "8px 10px", fontSize: 11, color: colors.error }}>
              {explain.errors.get(focus.messageId)}
            </div>
          )}
        </div>
      </div>
    </ChannelContext.Provider>
  );
}

function ExplanationCard({
  channelId,
  explanation: e,
  highlighted,
  busy,
  error,
  onReexplain,
}: {
  channelId: string;
  explanation: Explanation;
  highlighted: boolean;
  busy: boolean;
  error?: string;
  onReexplain: () => void;
}) {
  const { colors } = useTheme();
  const pending = explanationPending(e);
  const href = e.message_row_id ? inAppHref(messageLink(channelId, e.message_row_id)) : null;
  const prompt = snippetLine(e.prompt);
  const reply = snippetLine(e.reply);
  const statusColor = e.status === "failed" ? colors.error : pending ? colors.warning : colors.textDim;

  return (
    <div
      data-testid="explanation"
      data-explain-msg={e.message_id}
      data-status={e.status}
      data-highlighted={highlighted ? "true" : undefined}
      style={{
        padding: "8px 10px",
        borderBottom: `1px solid ${colors.border}`,
        fontSize: 12,
        color: colors.textLight,
        outline: highlighted ? `2px solid ${colors.active}` : "none",
        outlineOffset: -2,
        animation: highlighted ? "loop-explain-blink 0.5s ease-in-out 2" : undefined,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{ color: colors.textDim, display: "flex" }}>
          <ExplainIcon size={12} />
        </span>
        <span style={{ fontSize: 11, color: colors.textDim }}>{formatMessageTimestamp(e.updated_at)}</span>
        <span data-testid="explanation-status" style={{ fontFamily: fonts.mono, fontSize: 10, color: statusColor, border: `1px solid ${statusColor}`, borderRadius: 8, padding: "0 6px" }}>
          {e.status === "running" ? "explaining…" : e.status}
        </span>
        <div style={{ flex: 1 }} />
        {href && (
          <a data-testid="explanation-goto" href={href} title="Show the turn's last message in the chat" style={{ fontSize: 11, color: colors.active, textDecoration: "none" }}>
            Go to message
          </a>
        )}
        <button
          data-testid="explanation-reexplain"
          onClick={onReexplain}
          disabled={pending || busy}
          title={pending ? "Being explained" : "Explain this turn again, replacing this explanation"}
          style={{
            background: "transparent",
            border: `1px solid ${colors.active}`,
            color: colors.active,
            cursor: pending || busy ? "default" : "pointer",
            opacity: pending || busy ? 0.5 : 1,
            padding: "2px 8px",
            fontSize: 10,
            fontFamily: fonts.mono,
            lineHeight: 1.4,
            borderRadius: 10,
          }}
        >
          Re-explain
        </button>
      </div>
      {(prompt || reply) && (
        <div style={{ marginTop: 6, fontSize: 11, color: colors.textDim, display: "flex", flexDirection: "column", gap: 2 }}>
          {prompt && (
            <div data-testid="explanation-prompt" style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
              <b>Prompt:</b> {prompt}
            </div>
          )}
          {reply && (
            <div data-testid="explanation-reply" style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
              <b>Reply:</b> {reply}
            </div>
          )}
        </div>
      )}
      {error && (
        <div data-testid="explain-request-error" style={{ marginTop: 6, fontSize: 11, color: colors.error }}>
          {error}
        </div>
      )}
      {e.status === "failed" && e.error && <div style={{ marginTop: 6, fontSize: 11, color: colors.error }}>{e.error}</div>}
      {pending && (
        <div style={{ marginTop: 6, fontSize: 11, color: colors.textDim }}>
          {e.status === "running" ? "A forked session is writing up this turn…" : "Queued behind the channel's other explanations…"}
        </div>
      )}
      {e.status === "done" && e.content && (
        <div data-testid="explanation-content" style={{ marginTop: 6 }}>
          <MarkdownContent content={e.content} />
        </div>
      )}
    </div>
  );
}
