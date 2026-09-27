import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { isOpenProposal, learnBadgeLabel } from "../chat/learnState";

/**
 * The layouts bar's Learn label: lit while a learn pass runs or proposals
 * wait, dim once the channel has a learn thread to look back at. A click
 * opens or closes the Learn drawer.
 */
export function LearnBadge({ learn, open, onToggle }: { learn: LearnView; open: boolean; onToggle: () => void }) {
  const { colors } = useTheme();
  const label = learnBadgeLabel(learn.running, learn.proposals.filter(isOpenProposal).length);
  if (!label && !learn.learnChannelId && !open) return null;
  const lit = label !== null;
  return (
    <button
      data-testid="learn-badge"
      data-running={learn.running ? "true" : "false"}
      onClick={onToggle}
      title={open ? "Hide the Learn drawer" : "Show the Learn drawer"}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        whiteSpace: "nowrap",
        background: open ? colors.hoverBg : "none",
        border: `1px solid ${lit ? colors.active : colors.border}`,
        color: lit ? colors.active : colors.textDim,
        cursor: "pointer",
        padding: "2px 8px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 10,
      }}
    >
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 18h6" />
        <path d="M10 22h4" />
        <path d="M12 2a7 7 0 0 0-4 12.7V17h8v-2.3A7 7 0 0 0 12 2z" />
      </svg>
      {label ?? "learn"}
    </button>
  );
}
