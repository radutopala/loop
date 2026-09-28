import { paneBoxStyle, SplitDivider } from "../../splitPane/SplitPaneLayout";
import { useTheme } from "../../ThemeContext";
import { LoopInfinityIcon } from "../LoopInfinityIcon";

// How long opening or closing the Learn view takes.
export const LEARN_SPLIT_MS = 150;

// The slot in the Learn view's chat header, for the Learn badge that closes it.
export const LEARN_SPLIT_SLOT_ID = "learn-split";

interface LearnSplitProps {
  /** Fades the rest in; false fades it out, and the parent unmounts it once
   * that ends. */
  shown: boolean;
  /** Animation length in ms; 0 skips it. */
  ms: number;
  /** A learn pass is running: the logo between the panes animates. */
  running: boolean;
  chatHeader: React.ReactNode;
  chat: React.ReactNode;
  learnPane: React.ReactNode;
}

/**
 * The Learn view: the chat and its Learn pane split half and half, full
 * height over the layout, joined by the Loop logo on their seam. They're
 * panes like the layout's (same box, same divider), but the view is its own
 * layer: the layout stays mounted, and still, underneath. The chat's half
 * doesn't animate: the chat moves into it as the view mounts and back out as
 * it unmounts. The Learn half and the logo fade in and out over the layout.
 */
export function LearnSplit({ shown, ms, running, chatHeader, chat, learnPane }: LearnSplitProps) {
  const { colors } = useTheme();
  const gap = colors.islandGap || 4;
  const pane: React.CSSProperties = {
    flex: "1 1 0%",
    minWidth: 0,
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
    position: "relative",
    ...paneBoxStyle(colors),
  };
  const fade: React.CSSProperties = { opacity: shown ? 1 : 0, transition: ms ? `opacity ${ms}ms ease` : "none" };

  return (
    <div data-testid="learn-split" style={{ position: "absolute", inset: 0, zIndex: 20, overflow: "hidden", display: "flex" }}>
      {/* The chat's half, with the divider, doesn't fade: nothing under the
          chat may animate as it moves in, or the browser lifts the chat onto
          a layer of its own, painted afresh, and it flickers. Its background
          covers the layout around the chat card at once. */}
      <div style={{ width: `calc(50% + ${gap / 2}px)`, flexShrink: 0, display: "flex", backgroundColor: colors.bg }}>
        <div data-testid="learn-split-chat" style={pane}>
          {chatHeader}
          <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, position: "relative" }}>{chat}</div>
        </div>
        <SplitDivider vertical={false} />
      </div>
      {/* The Learn half fades in over the layout, and out. */}
      <div style={{ flex: 1, minWidth: 0, display: "flex", backgroundColor: colors.bg, ...fade }}>
        <div style={pane}>{learnPane}</div>
      </div>
      {/* The logo joining the two panes, on their seam. */}
      <div
        data-testid="learn-split-logo"
        style={{
          position: "absolute",
          left: "50%",
          top: "50%",
          width: 40,
          height: 40,
          transform: "translate(-50%, -50%)",
          ...fade,
          borderRadius: "50%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: colors.sidebar,
          border: `1px solid ${colors.border}`,
          boxShadow: "0 2px 12px rgba(0, 0, 0, 0.35)",
          pointerEvents: "none",
        }}
      >
        <div style={{ zoom: 1.5, display: "flex" }}>
          <LoopInfinityIcon color={running ? undefined : colors.textDim} animated={running} isDark={colors.isDark} />
        </div>
      </div>
    </div>
  );
}
