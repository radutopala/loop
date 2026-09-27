import { useCallback, useEffect, useState } from "react";
import { fetchLearnState, type LearnState, setLearn } from "../../api/learn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";
import { learnEffective, learnToggleTitle } from "./learnState";

/**
 * Composer switch for the channel's learn pass. It's sticky per channel: a
 * click stores on/off on the channel, which then applies from its next run.
 */
export function LearnToggle({ channelId }: { channelId: string }) {
  const { colors } = useTheme();
  const [learn, setLearnValue] = useState<LearnState["learn"]>("");
  const [defaultLearn, setDefaultLearn] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoaded(false);
    fetchLearnState(channelId)
      .then((st) => {
        if (cancelled) return;
        setLearnValue(st.learn);
        setDefaultLearn(st.default_learn);
        setLoaded(true);
      })
      .catch(logErr("fetching learn state"));
    return () => {
      cancelled = true;
    };
  }, [channelId]);

  const on = learnEffective(learn, defaultLearn);
  const toggle = useCallback(async () => {
    const prev = learn;
    const next = on ? "off" : "on";
    setLearnValue(next);
    try {
      await setLearn(channelId, next);
    } catch (e) {
      setLearnValue(prev);
      logErr("setting learn")(e);
    }
  }, [channelId, learn, on]);

  if (!loaded) return null;
  return (
    <button
      data-testid="learn-toggle"
      data-on={on ? "true" : "false"}
      aria-pressed={on}
      onClick={toggle}
      title={learnToggleTitle(learn, defaultLearn)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        height: 24,
        padding: "0 8px",
        marginRight: 6,
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
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 18h6" />
        <path d="M10 22h4" />
        <path d="M12 2a7 7 0 0 0-4 12.7V17h8v-2.3A7 7 0 0 0 12 2z" />
      </svg>
      learn
    </button>
  );
}
