import { useContext } from "react";
import { ExplainContext } from "../../hooks/useExplain";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ExplainIcon } from "./ExplainIcon";
import { explainToggleTitle } from "./explainState";
import { HoverTip } from "./HoverTip";
import { learnEffective } from "./learnState";

/**
 * Composer switch for explaining each turn. It's sticky per channel, like
 * the Learn switch beside it: a click stores on/off on the channel, which
 * then applies from its next run. Channels never explained (Slack, Discord,
 * task threads) don't show it.
 */
export function ExplainToggle() {
  const { colors } = useTheme();
  const view = useContext(ExplainContext);
  if (!view || !view.loaded || !view.available) return null;
  const { explain, defaultExplain } = view;
  const on = learnEffective(explain, defaultExplain);
  const toggle = () => view.setExplain(on ? "off" : "on");

  return (
    <HoverTip text={explainToggleTitle(explain, defaultExplain)}>
      <button
        data-testid="explain-toggle"
        aria-label="Explain"
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
        <ExplainIcon size={12} />
      </button>
    </HoverTip>
  );
}
