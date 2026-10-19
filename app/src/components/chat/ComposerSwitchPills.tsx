import { useContext } from "react";
import { ComposerSwitchesContext } from "../../hooks/useComposerSwitches";
import { useTheme } from "../../ThemeContext";
import { switchKey, switchLook, switchTip } from "./composerSwitches";
import { HoverTip } from "./HoverTip";

/**
 * The composer switches plugins put in the chat, beside Learn and Explain:
 * a round pill each, drawing the plugin's icon in the pill's color. Off is
 * dim, on the accent, a failed switch red; a switch its plugin is working
 * on has a dashed border.
 */
export function ComposerSwitchPills() {
  const { colors } = useTheme();
  const view = useContext(ComposerSwitchesContext);
  if (!view || view.switches.length === 0) return null;
  const color = { off: colors.textDim, on: colors.active, busy: colors.active, error: colors.error };

  return (
    <>
      {view.switches.map((sw) => {
        const key = switchKey(sw);
        const pending = view.pending.has(key);
        const error = view.errors.get(key);
        const look = switchLook(sw, pending, !!error);
        const c = look === "busy" && !sw.on ? colors.textDim : color[look];
        return (
          <HoverTip key={key} text={switchTip(sw, error)}>
            <button
              data-testid={`composer-switch-${key}`}
              aria-label={sw.label || sw.switch_id}
              aria-pressed={sw.on}
              data-look={look}
              disabled={pending}
              onClick={() => void view.turn(sw)}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                width: 24,
                height: 24,
                padding: 0,
                flexShrink: 0,
                background: "transparent",
                border: `1px ${look === "busy" ? "dashed" : "solid"} ${look === "off" ? colors.border : c}`,
                borderRadius: 12,
                color: c,
                cursor: pending ? "progress" : "pointer",
              }}
            >
              {sw.icon ? (
                <span
                  aria-hidden
                  style={{
                    width: 12,
                    height: 12,
                    backgroundColor: "currentColor",
                    maskImage: `url("${sw.icon}")`,
                    maskSize: "contain",
                    maskRepeat: "no-repeat",
                    maskPosition: "center",
                    WebkitMaskImage: `url("${sw.icon}")`,
                    WebkitMaskSize: "contain",
                    WebkitMaskRepeat: "no-repeat",
                    WebkitMaskPosition: "center",
                  }}
                />
              ) : (
                <span style={{ fontSize: 10, fontWeight: 600 }}>{(sw.label || sw.switch_id).slice(0, 1).toUpperCase()}</span>
              )}
            </button>
          </HoverTip>
        );
      })}
    </>
  );
}
