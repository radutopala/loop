import { useCallback, useEffect, useRef, useState } from "react";
import { searchChannelMessages } from "../../api/search";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { matchCountLabel, stepMatch } from "./chatFind";

// Long enough that typing a word runs one search, not one per letter.
const SEARCH_DEBOUNCE_MS = 250;

interface ChatFindBarProps {
  channelId: string;
  /** Changes each time the bar is asked to open, so an open bar takes focus again. */
  focusKey: number;
  /** Scrolls a match into view, paging older messages in as needed. Must be stable. */
  onJump: (messageId: number, term: string) => void;
  onClose: () => void;
}

// ChatFindBar finds text in the channel's messages. The search runs on the
// server, so it covers the whole history, not only the pages loaded; stepping
// to a match scrolls to it the way a message link does.
export function ChatFindBar({ channelId, focusKey, onJump, onClose }: ChatFindBarProps) {
  const { colors } = useTheme();
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  // null until the current term has been searched.
  const [matches, setMatches] = useState<number[] | null>(null);
  const [active, setActive] = useState(0);
  const term = query.trim();

  useEffect(() => {
    inputRef.current?.select();
  }, [focusKey]);

  useEffect(() => {
    if (term === "") {
      setMatches(null);
      return;
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      searchChannelMessages(channelId, term, ctrl.signal)
        .then((ids) => {
          setMatches(ids);
          setActive(0);
          if (ids[0] !== undefined) onJump(ids[0], term);
        })
        .catch(() => {
          if (!ctrl.signal.aborted) setMatches([]);
        });
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [channelId, term, onJump]);

  // The position is kept in a ref too, so clicks landing before a re-render
  // each step from the one before rather than all from the same match.
  const activeRef = useRef(0);
  activeRef.current = active;
  const step = useCallback(
    (delta: number) => {
      if (!matches || matches.length === 0) return;
      const next = stepMatch(activeRef.current, delta, matches.length);
      activeRef.current = next;
      setActive(next);
      onJump(matches[next]!, term);
    },
    [matches, term, onJump],
  );

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "Enter") {
      e.preventDefault();
      step(e.shiftKey ? -1 : 1);
    }
  };

  const count = matches?.length ?? 0;
  const btnStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: 24,
    height: 24,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textMuted,
    cursor: "pointer",
    padding: 0,
    flexShrink: 0,
  };

  return (
    <div
      data-testid="chat-find-bar"
      style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 12px", background: colors.surface, borderBottom: `1px solid ${colors.border}`, flexShrink: 0 }}
    >
      <input
        ref={inputRef}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Find in chat"
        data-testid="chat-find-input"
        style={{
          flex: 1,
          minWidth: 0,
          background: colors.bg,
          border: `1px solid ${colors.inputBorder}`,
          borderRadius: 4,
          color: colors.textLight,
          fontFamily: fonts.mono,
          fontSize: 12,
          padding: "3px 8px",
          outline: "none",
        }}
      />
      {term !== "" && matches !== null && (
        <span data-testid="chat-find-count" style={{ fontFamily: fonts.mono, fontSize: 11, color: count > 0 ? colors.textDim : colors.error, flexShrink: 0 }}>
          {matchCountLabel(active, count)}
        </span>
      )}
      <button style={{ ...btnStyle, opacity: count === 0 ? 0.3 : 1 }} disabled={count === 0} onClick={() => step(1)} title="Older match (Enter)" data-testid="chat-find-older">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 6.5L5 3.5L7.5 6.5" />
        </svg>
      </button>
      <button style={{ ...btnStyle, opacity: count === 0 ? 0.3 : 1 }} disabled={count === 0} onClick={() => step(-1)} title="Newer match (Shift+Enter)" data-testid="chat-find-newer">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 3.5L5 6.5L7.5 3.5" />
        </svg>
      </button>
      <button style={btnStyle} onClick={onClose} title="Close (Esc)" data-testid="chat-find-close">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
          <path d="M2 2L8 8M8 2L2 8" />
        </svg>
      </button>
    </div>
  );
}
