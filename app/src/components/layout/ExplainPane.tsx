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
 * The channel's explanations, as a pane beside its chat in the Explain view
 * (the Learn view's, see LearnSplit): one card per explained turn, newest
 * first, with the start of its prompt and reply, a link back to the turn's
 * last message, the write-up, and Re-explain. The card a turn's Explain
 * action asked for is brought into view and outlined.
 */
export function ExplainPane({ channelId, explain, onClose }: { channelId: string; explain: ExplainView; onClose: () => void }) {
  const { colors } = useTheme();
  const listRef = useRef<HTMLDivElement>(null);
  const [highlighted, setHighlighted] = useState<string | null>(null);
  const { focus, explanations } = explain;
  const focusedShown = !!focus && explanations.some((e) => e.message_id === focus.messageId);
  const running = explanations.some(explanationPending);

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
      <div data-testid="explain-pane" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden", fontFamily: fonts.sans }}>
        {/* Header, like the Learn pane's; docked on a canvas, it drags the pair. */}
        <div
          data-learn-pane-header
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "2px 8px",
            height: 22,
            boxSizing: "border-box",
            backgroundColor: colors.surface,
            borderBottom: `1px solid ${colors.border}`,
            flexShrink: 0,
          }}
        >
          <span style={{ padding: "1px 4px", borderRadius: 3, fontSize: 10, fontWeight: 500, color: colors.textLight, backgroundColor: colors.panelLabelBg }}>Explain</span>
          <span style={{ color: colors.textDim, fontSize: 10, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
            {running ? "explaining a turn…" : "write-ups of the chat's turns"}
          </span>
          <div style={{ flex: 1 }} />
          <button
            data-testid="explain-close"
            aria-label="Close the Explain view"
            onClick={onClose}
            title="Close the Explain view"
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", lineHeight: 1, display: "flex", alignItems: "center", borderRadius: 2 }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = colors.hoverBg;
              e.currentTarget.style.color = colors.textLight;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.backgroundColor = "transparent";
              e.currentTarget.style.color = colors.textDim;
            }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <style>{"@keyframes loop-explain-blink { 50% { outline-color: transparent; } }"}</style>
        <div ref={listRef} style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
          {explanations.length === 0 ? (
            <div data-testid="explain-empty" style={{ padding: 16, color: colors.textDim, fontSize: 12, lineHeight: 1.5 }}>
              No explanations yet. Click Explain at the end of a turn, or turn explain on in the composer to explain every turn.
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
