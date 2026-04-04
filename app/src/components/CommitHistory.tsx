import { useCallback } from "react";
import type { CommitEntry } from "../api/loopApi";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";

interface CommitHistoryProps {
  commits: CommitEntry[];
  commitsLoading: boolean;
  onLoadMore: () => void;
}

export function CommitHistory({ commits, commitsLoading, onLoadMore }: CommitHistoryProps) {
  const { colors } = useTheme();

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 100) {
      onLoadMore();
    }
  }, [onLoadMore]);

  return (
    <div style={{ flex: 1, overflow: "auto", minHeight: 0 }} onScroll={handleScroll}>
      {commitsLoading && commits.length === 0 && (
        <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {!commitsLoading && commits.length === 0 && (
        <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>No commits</div>
      )}
      {commits.map((c) => (
        <div
          key={c.hash}
          style={{
            padding: "6px 12px",
            borderBottom: `1px solid ${colors.border}`,
            fontSize: 12,
            cursor: "default",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.background = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; }}
        >
          <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
            <span style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.active, flexShrink: 0 }}>{c.short}</span>
            <span style={{ color: colors.textLight, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>{c.subject}</span>
          </div>
          <div style={{ display: "flex", gap: 8, fontSize: 11, color: colors.textDim, marginTop: 2 }}>
            <span>{c.author}</span>
            <span>{new Date(c.date).toLocaleDateString()}</span>
          </div>
        </div>
      ))}
      {commitsLoading && commits.length > 0 && (
        <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 11, textAlign: "center" }}>Loading more...</div>
      )}
    </div>
  );
}
