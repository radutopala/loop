import { useContext } from "react";
import { LearnContext, LearnDrawerContext } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { isOpenProposal, learnBadgeLabel } from "./learnState";

/**
 * The chat's Learn label, next to its find button: lit while a learn pass
 * runs or proposals wait, dim once the channel has a learn thread to look
 * back at. A click opens or closes the Learn drawer. Only the layout's chat
 * for the selected channel shows it (the drawer's own chat has no context).
 */
export function LearnBadge({ channelId }: { channelId: string }) {
  const { colors } = useTheme();
  const learn = useContext(LearnContext);
  const drawer = useContext(LearnDrawerContext);
  if (!learn || !drawer || learn.channelId !== channelId) return null;
  const label = learnBadgeLabel(learn.running, learn.proposals.filter(isOpenProposal).length);
  if (!label && !learn.learnChannelId && !drawer.open) return null;
  const lit = label !== null;
  return (
    <button
      data-testid="learn-badge"
      data-running={learn.running ? "true" : "false"}
      onClick={drawer.toggle}
      title={drawer.open ? "Hide the Learn drawer" : "Show the Learn drawer"}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 24,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        background: drawer.open ? colors.hoverBg : colors.bg,
        border: `1px solid ${lit ? colors.active : colors.border}`,
        color: lit ? colors.active : colors.textDim,
        cursor: "pointer",
        padding: "0 8px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 12,
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
