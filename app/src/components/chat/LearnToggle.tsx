import { useContext } from "react";
import { LearnContext } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { HoverTip } from "./HoverTip";
import { LearnIcon } from "./LearnIcon";
import { learnEffective, learnToggleTitle } from "./learnState";

/**
 * Composer switch for the channel's learn pass. It's sticky per channel: a
 * click stores on/off on the channel, which then applies from its next run.
 * Its state is the layout's LearnView (see useLearn), so it follows changes
 * made in other windows; Slack and Discord channels, which never learn,
 * don't show it, nor does a learn thread's own composer (it has no
 * LearnView: a learn thread doesn't learn from itself).
 */
export function LearnToggle() {
  const { colors } = useTheme();
  const view = useContext(LearnContext);
  if (!view || !view.loaded || !view.available) return null;
  const { learn, defaultLearn } = view;
  const on = learnEffective(learn, defaultLearn);
  const toggle = () => view.setLearn(on ? "off" : "on");

  return (
    <HoverTip text={learnToggleTitle(learn, defaultLearn)}>
      <button
        data-testid="learn-toggle"
        aria-label="Learn"
        data-on={on ? "true" : "false"}
        aria-pressed={on}
        onClick={toggle}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: 24,
          height: 24,
          padding: 0,
          flexShrink: 0,
          background: "transparent",
          border: `1px solid ${on ? colors.active : colors.border}`,
          borderRadius: 12,
          color: on ? colors.active : colors.textDim,
          cursor: "pointer",
          fontFamily: fonts.mono,
          fontSize: 10,
        }}
      >
        <LearnIcon size={12} />
      </button>
    </HoverTip>
  );
}
