import { fonts } from "../../theme";

interface StatusPillProps {
  label: string;
  color: string;
  /** Only a plugin badge has a tooltip: the status pills leave theirs to the row's info popup. */
  title?: string;
  marginLeft?: number | string;
  /** How far under the top of a closed deck the pill sits; 0 or unset is a pill on its own. */
  depth?: number;
  /** Fills a deck's pills, so the top one hides the label of the ones under it. */
  background?: string;
}

/** How far each pill under the top of a closed deck peeks out, right and up. */
const DECK_STEP_X = 3;
const DECK_STEP_Y = 1;

export function StatusPill({ label, color, title, marginLeft, depth = 0, background }: StatusPillProps) {
  return (
    <span
      title={title}
      data-pill={label}
      style={{
        flexShrink: 0,
        fontSize: 9,
        fontFamily: fonts.mono,
        lineHeight: 1,
        padding: "2px 4px",
        borderRadius: 3,
        color: depth > 0 ? "transparent" : color,
        border: `1px solid ${color}`,
        marginLeft,
        background,
        ...(background !== undefined && { gridArea: "1 / 1", transform: `translate(${depth * DECK_STEP_X}px, ${-depth * DECK_STEP_Y}px)` }),
      }}
    >
      {label}
    </span>
  );
}

interface PillDeckProps {
  pills: { key: string; label: string; color: string }[];
  /** Lays the pills out side by side, as while the row is hovered. */
  open: boolean;
  /** The row's background, filled behind every closed pill. */
  background: string;
  marginLeft?: number | string;
  /** Space between open pills, for a row that has no flex gap of its own. */
  spacing?: number;
}

/**
 * The pills that need you, stacked as a deck: the first one, the most urgent,
 * on top, with the edges of the rest peeking out behind it. A row with one
 * pill, or an open row, shows them side by side.
 */
export function PillDeck({ pills, open, background, marginLeft, spacing }: PillDeckProps) {
  if (pills.length === 0) return null;
  if (open || pills.length === 1) {
    return pills.map((p, i) => <StatusPill key={p.key} label={p.label} color={p.color} marginLeft={i === 0 ? marginLeft : spacing} />);
  }
  return (
    <span data-pill-deck={pills.length} style={{ display: "inline-grid", flexShrink: 0, marginLeft, paddingRight: (pills.length - 1) * DECK_STEP_X }}>
      {pills
        .map((p, i) => <StatusPill key={p.key} label={p.label} color={p.color} depth={i} background={background} />)
        // Later siblings paint on top, so the deck's top pill goes last.
        .reverse()}
    </span>
  );
}
