import { useCallback, useRef } from "react";
import { paneBoxStyle, SplitDivider } from "../../splitPane/SplitPaneLayout";
import { useTheme } from "../../ThemeContext";
import { LoopInfinityIcon } from "../LoopInfinityIcon";

/** Which side view the chat is split with: the Learn pane, the Explain one,
 *  or a shell in the chat's container. */
export type SideView = "learn" | "explain" | "shell";

export const SIDE_VIEW_LABEL: Record<SideView, string> = { learn: "Learn view", explain: "Explain view", shell: "Shell view" };

// How long opening or closing the Learn view takes.
export const LEARN_SPLIT_MS = 150;

// The slot in the Learn view's chat header, for the Learn and Explain badges that close it.
export const LEARN_SPLIT_SLOT_ID = "learn-split";

interface LearnSplitProps {
  /** Fades the rest in; false fades it out, and the parent unmounts it once
   * that ends. */
  shown: boolean;
  /** Animation length in ms; 0 skips it. */
  ms: number;
  /** The pane beside the chat: Learn's, or Explain's (same view). */
  view?: SideView;
  /** A learn pass (or an explanation) is running: the logo between the
   * panes animates. */
  running: boolean;
  chatHeader: React.ReactNode;
  chat: React.ReactNode;
  learnPane: React.ReactNode;
  ref?: React.Ref<HTMLDivElement>;
  /** Names the view for tests and screen readers instead of view's. */
  testId?: string;
  label?: string;
  /** Leaves the seam's logo out, for a caller that draws it over a layer
   *  lying on the view. */
  noLogo?: boolean;
  /** The chat's share of the width, half by default. */
  split?: number;
  /** Makes the divider a handle: dragging it reports the chat's new share. */
  onSplitChange?: (split: number) => void;
}

// How narrow either half of a resizable view may get, as a share of it.
const MIN_SPLIT = 0.15;

/** The chat's share of the view, kept between MIN_SPLIT and its mirror. */
export function clampSplit(split: number): number {
  return Math.max(MIN_SPLIT, Math.min(1 - MIN_SPLIT, split));
}

/**
 * The Learn view: the chat and its Learn pane split half and half, full
 * height over the layout, joined by the Loop logo on their seam. They're
 * panes like the layout's (same box, same divider), but the view is its own
 * layer: the layout stays mounted, and still, underneath. The chat's half
 * doesn't animate: the chat moves into it as the view mounts and back out as
 * it unmounts. The Learn half and the logo fade in and out over the layout.
 */
export function LearnSplit({ shown, ms, view = "learn", running, chatHeader, chat, learnPane, ref, testId = "learn-split", label, noLogo, split = 0.5, onSplitChange }: LearnSplitProps) {
  const { colors } = useTheme();
  const gap = colors.islandGap || 4;
  const boxRef = useRef<HTMLDivElement | null>(null);
  const setBox = useCallback(
    (el: HTMLDivElement | null) => {
      boxRef.current = el;
      if (typeof ref === "function") ref(el);
      else if (ref) ref.current = el;
    },
    [ref],
  );
  const onDivider = useCallback(
    (e: React.MouseEvent) => {
      const box = boxRef.current;
      if (!box || !onSplitChange) return;
      e.preventDefault();
      const rect = box.getBoundingClientRect();
      const onMove = (ev: MouseEvent) => onSplitChange(clampSplit((ev.clientX - rect.left) / rect.width));
      const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        document.querySelectorAll("iframe").forEach((f) => (f.style.pointerEvents = ""));
      };
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      // Frames would take the mouse moves over them.
      document.querySelectorAll("iframe").forEach((f) => (f.style.pointerEvents = "none"));
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    },
    [onSplitChange],
  );
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
    <div
      ref={setBox}
      data-testid={testId}
      data-view={view}
      role="region"
      aria-label={label ?? SIDE_VIEW_LABEL[view]}
      style={{ position: "absolute", inset: 0, zIndex: 20, overflow: "hidden", display: "flex" }}
    >
      {/* The chat's half, with the divider, doesn't fade: nothing under the
          chat may animate as it moves in, or the browser lifts the chat onto
          a layer of its own, painted afresh, and it flickers. Its background
          covers the layout around the chat card at once. */}
      <div style={{ width: `calc(${split * 100}% + ${gap / 2}px)`, flexShrink: 0, display: "flex", backgroundColor: colors.bg }}>
        <div data-testid={`${testId}-chat`} style={pane}>
          {chatHeader}
          <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, position: "relative" }}>{chat}</div>
        </div>
        <SplitDivider vertical={false} onMouseDown={onSplitChange ? onDivider : undefined} />
      </div>
      {/* The Learn half fades in over the layout, and out. */}
      <div style={{ flex: 1, minWidth: 0, display: "flex", backgroundColor: colors.bg, ...fade }}>
        <div style={pane}>{learnPane}</div>
      </div>
      {/* The logo joining the two panes, on their seam. */}
      {!noLogo && <SplitSeamLogo testId={`${testId}-logo`} running={running} split={split} style={fade} />}
    </div>
  );
}

/**
 * The Loop logo on the seam of two panes, split at split (half and half by
 * default) of their parent; it animates while running. testId names it;
 * style adds to its own (a fade).
 */
export function SplitSeamLogo({ testId, running, split = 0.5, style }: { testId: string; running: boolean; split?: number; style?: React.CSSProperties }) {
  const { colors } = useTheme();
  return (
    <div
      data-testid={testId}
      style={{
        position: "absolute",
        left: `${split * 100}%`,
        top: "50%",
        width: 40,
        height: 40,
        transform: "translate(-50%, -50%)",
        ...style,
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
  );
}
