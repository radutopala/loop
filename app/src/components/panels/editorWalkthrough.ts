import { type EditorState, type Extension, Facet, Prec, type Range, StateEffect, StateField, Transaction } from "@codemirror/state";
import { Decoration, type DecorationSet, EditorView, keymap, WidgetType } from "@codemirror/view";
import type { WalkthroughStep } from "../../api/walkthroughs";
import { type ColorPalette, fonts } from "../../theme";
import { renderMarkdownSafe } from "../../utils/markdownSafe";
import { locateStep, type StepRange } from "./walkthroughAnchor";

// A walkthrough's step over the editor's file: its lines highlighted, and a
// card above them with what the step says, Prev / Play / Next / Close and
// Ask. The
// card is a block widget between the lines, so the code around it stays
// selectable and editable.

/** The step a walkthrough shows over the editor's file. */
export interface WalkthroughCard {
  walkthroughTitle: string;
  /** From 0. */
  index: number;
  count: number;
  step: WalkthroughStep;
  /** Why the step asked for last isn't this one. */
  notice?: string;
  /** Whether it moves on to the next step by itself. */
  playing?: boolean;
}

/** The step's lines as they are now, for Ask. */
export interface WalkthroughRange {
  start_line: number;
  end_line: number;
  text: string;
}

/** What the card's buttons and keys do. */
export interface WalkthroughHandlers {
  prev(): void;
  next(): void;
  close(): void;
  ask(range: WalkthroughRange): void;
  /** Plays the walkthrough, or pauses it while it plays. */
  togglePlay(): void;
  /** Pauses it: the user edited the file under a step. */
  pause(): void;
}

/** Shows a step over the file, or, with null, none. */
export const setWalkthroughCard = StateEffect.define<WalkthroughCard | null>();

interface Shown {
  card: WalkthroughCard;
  /** The start of the step's first line and the end of its last. */
  from: number;
  to: number;
  status: StepRange["status"];
  decorations: DecorationSet;
}

/** The lines from..to of state, 1-based. */
function lineRange(state: EditorState, from: number, to: number): WalkthroughRange {
  const start = state.doc.lineAt(from);
  const end = state.doc.lineAt(to);
  return { start_line: start.number, end_line: end.number, text: state.sliceDoc(start.from, end.to) };
}

class CardWidget extends WidgetType {
  constructor(
    readonly card: WalkthroughCard,
    readonly status: StepRange["status"],
    readonly handlers: WalkthroughHandlers,
  ) {
    super();
  }

  eq(other: CardWidget) {
    return other.card === this.card && other.status === this.status;
  }

  toDOM(view: EditorView) {
    const { card, status, handlers } = this;
    const el = (tag: string, cls: string, text?: string) => {
      const e = document.createElement(tag);
      e.className = cls;
      if (text !== undefined) e.textContent = text;
      return e;
    };
    const root = el("div", "cm-walkthrough-card");
    root.setAttribute("data-testid", "walkthrough-card");
    const head = el("div", "cm-walkthrough-head");
    head.append(el("span", "cm-walkthrough-title", card.walkthroughTitle), el("span", "cm-walkthrough-count", `Step ${card.index + 1} of ${card.count}`));
    root.append(head);
    if (card.step.title) root.append(el("div", "cm-walkthrough-stepTitle", card.step.title));
    if (card.step.body) {
      const body = el("div", "cm-walkthrough-body");
      body.innerHTML = renderMarkdownSafe(card.step.body);
      root.append(body);
    }
    if (status === "changed") root.append(el("div", "cm-walkthrough-note", "This code changed since the walkthrough was made; the step shows where it was."));
    if (card.notice) root.append(el("div", "cm-walkthrough-note", card.notice));
    const actions = el("div", "cm-walkthrough-actions");
    const button = (label: string, title: string, onClick: () => void, disabled = false) => {
      const b = el("button", "cm-walkthrough-btn", label) as HTMLButtonElement;
      b.type = "button";
      b.title = title;
      b.disabled = disabled;
      b.onclick = (e) => {
        e.preventDefault();
        onClick();
      };
      return b;
    };
    actions.append(
      button("Ask about this step", "Ask the agent about this step's code", () => {
        const shown = view.state.field(walkthroughField, false);
        if (shown) handlers.ask(lineRange(view.state, shown.from, shown.to));
      }),
      el("span", "cm-walkthrough-spacer"),
      button("Prev", "Previous step (Alt+←)", handlers.prev, card.index === 0),
      card.playing ? button("Pause", "Stop moving on by itself", handlers.togglePlay) : button("Play", "Move on to the next step by itself", handlers.togglePlay, card.count < 2),
      button("Next", "Next step (Alt+→)", handlers.next, card.index >= card.count - 1),
      button("Close", "Close the walkthrough (Esc)", handlers.close),
    );
    root.append(actions);
    return root;
  }

  // The card's clicks and selections are its own, not the editor's.
  ignoreEvent() {
    return true;
  }
}

function decorate(state: EditorState, card: WalkthroughCard, range: StepRange, handlers: WalkthroughHandlers): Shown {
  const first = state.doc.line(range.start);
  const last = state.doc.line(range.end);
  const decos: Range<Decoration>[] = [Decoration.widget({ widget: new CardWidget(card, range.status, handlers), block: true, side: -1 }).range(first.from)];
  for (let n = range.start; n <= range.end; n++) {
    const cls = ["cm-walkthroughLine", n === range.start ? "cm-walkthroughLine-first" : "", n === range.end ? "cm-walkthroughLine-last" : ""].filter(Boolean).join(" ");
    decos.push(Decoration.line({ class: cls }).range(state.doc.line(n).from));
  }
  return { card, from: first.from, to: last.to, status: range.status, decorations: Decoration.set(decos, true) };
}

const noop = () => {};

/** The handlers of the editor's card: one editor's, given by its
 *  walkthroughExtension. */
const handlersFacet = Facet.define<WalkthroughHandlers, WalkthroughHandlers>({
  combine: (values) => values[0] ?? { prev: noop, next: noop, close: noop, ask: noop, togglePlay: noop, pause: noop },
});

const walkthroughField: StateField<Shown | null> = StateField.define<Shown | null>({
  create: () => null,
  update(shown, tr) {
    for (const e of tr.effects) {
      if (!e.is(setWalkthroughCard)) continue;
      const card = e.value;
      if (!card) return null;
      const handlers = tr.state.facet(handlersFacet);
      const range = locateStep(tr.state.doc.toString(), card.step.start_line, card.step.end_line, card.step.anchor);
      return decorate(tr.state, card, range, handlers);
    }
    if (!shown || !tr.docChanged) return shown;
    // Edits move the step's lines with the text around them.
    const from = tr.changes.mapPos(shown.from, -1);
    return { ...shown, from, to: Math.max(from, tr.changes.mapPos(shown.to, 1)), decorations: shown.decorations.map(tr.changes) };
  },
  provide: (f) => EditorView.decorations.from(f, (shown) => shown?.decorations ?? Decoration.none),
});

/** How much of a selection selectedRange gives. */
export const MAX_SELECTED_CHARS = 4000;

/** The main selection of state with its lines (1-based), its text cut to
 *  MAX_SELECTED_CHARS; null when it's empty. */
export function selectedRange(state: EditorState): WalkthroughRange | null {
  const { from, to } = state.selection.main;
  if (from === to) return null;
  return { start_line: state.doc.lineAt(from).number, end_line: state.doc.lineAt(to).number, text: state.sliceDoc(from, Math.min(to, from + MAX_SELECTED_CHARS)) };
}

/** The step shown over the editor's file, as its lines are now, or null. */
export function shownWalkthroughRange(state: EditorState): WalkthroughRange | null {
  const shown = state.field(walkthroughField, false);
  return shown ? lineRange(state, shown.from, shown.to) : null;
}

/** The walkthrough layer of an editor; handlers are what its card's buttons
 *  and keys (Alt+←, Alt+→, Esc while a step shows) do. */
export function walkthroughExtension(handlers: WalkthroughHandlers): Extension {
  const whileShown = (fn: () => void) => (view: EditorView) => {
    if (!view.state.field(walkthroughField, false)) return false;
    fn();
    return true;
  };
  return [
    handlersFacet.of(handlers),
    walkthroughField,
    // Editing under a playing step pauses it, so the next step doesn't take
    // the file away mid-edit.
    EditorView.updateListener.of((u) => {
      if (u.docChanged && u.startState.field(walkthroughField, false) && u.transactions.some((tr) => tr.annotation(Transaction.userEvent) !== undefined)) handlers.pause();
    }),
    Prec.highest(
      keymap.of([
        { key: "Alt-ArrowLeft", run: whileShown(handlers.prev) },
        { key: "Alt-ArrowRight", run: whileShown(handlers.next) },
        { key: "Escape", run: whileShown(handlers.close) },
      ]),
    ),
  ];
}

/** The walkthrough layer's look, from the active palette. */
export function walkthroughTheme(palette: ColorPalette): Extension {
  const accent = palette.active;
  return EditorView.theme({
    ".cm-walkthroughLine": {
      backgroundColor: `${accent}1f`,
      boxShadow: `inset 3px 0 0 ${accent}`,
    },
    ".cm-walkthrough-card": {
      margin: "6px 12px 4px 4px",
      padding: "8px 12px",
      backgroundColor: palette.surface,
      color: palette.text,
      border: `1px solid ${palette.border}`,
      borderLeft: `3px solid ${accent}`,
      borderRadius: "6px",
      boxShadow: `0 2px 8px ${palette.shadow}`,
      fontFamily: fonts.sans,
      fontSize: "12px",
      lineHeight: "1.5",
      whiteSpace: "normal",
      cursor: "default",
      userSelect: "text",
    },
    ".cm-walkthrough-head": {
      display: "flex",
      alignItems: "baseline",
      gap: "8px",
      color: palette.textDim,
      fontSize: "11px",
    },
    ".cm-walkthrough-title": {
      flex: "1",
      minWidth: "0",
      overflow: "hidden",
      textOverflow: "ellipsis",
      whiteSpace: "nowrap",
    },
    ".cm-walkthrough-count": { flexShrink: "0" },
    ".cm-walkthrough-stepTitle": {
      marginTop: "2px",
      color: palette.textLight,
      fontWeight: "600",
      fontSize: "13px",
    },
    ".cm-walkthrough-body": { marginTop: "4px" },
    ".cm-walkthrough-body p": { margin: "4px 0" },
    ".cm-walkthrough-body ul, .cm-walkthrough-body ol": { margin: "4px 0", paddingLeft: "20px" },
    ".cm-walkthrough-body code": {
      fontFamily: fonts.mono,
      fontSize: "11px",
      backgroundColor: palette.codeBg,
      padding: "1px 4px",
      borderRadius: "3px",
    },
    ".cm-walkthrough-body pre": {
      backgroundColor: palette.codeBlockBg,
      padding: "6px 8px",
      borderRadius: "4px",
      overflowX: "auto",
      whiteSpace: "pre",
    },
    ".cm-walkthrough-body pre code": { backgroundColor: "transparent", padding: "0" },
    ".cm-walkthrough-body a": { color: accent },
    ".cm-walkthrough-note": {
      marginTop: "6px",
      color: palette.warning,
      fontSize: "11px",
    },
    ".cm-walkthrough-actions": {
      display: "flex",
      alignItems: "center",
      gap: "6px",
      marginTop: "8px",
    },
    ".cm-walkthrough-spacer": { flex: "1" },
    ".cm-walkthrough-btn": {
      background: "none",
      border: `1px solid ${palette.border}`,
      color: palette.textDim,
      borderRadius: "4px",
      padding: "2px 8px",
      fontSize: "11px",
      fontFamily: fonts.sans,
      lineHeight: "1.4",
      cursor: "pointer",
    },
    ".cm-walkthrough-btn:hover:not(:disabled)": {
      backgroundColor: palette.hoverBg,
      color: palette.textLight,
    },
    ".cm-walkthrough-btn:disabled": { opacity: "0.4", cursor: "default" },
  });
}
