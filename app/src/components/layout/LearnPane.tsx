import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { RootEntry } from "../../api/files";
import { fetchLearnProposalPreview, type LearnProposal, type LearnProposalPreview } from "../../api/learn";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";
import { ChatView } from "../chat/ChatView";
import { configPreviewRevision, editsProjectConfig, isSettledProposal, learnKindLabel, proposalCaveat, proposalDetail } from "../chat/learnState";
import { UnifiedDiff } from "../shared/UnifiedDiff";

interface LearnPaneProps {
  learn: LearnView;
  /** The learning channel is a worktree thread (renames leave its branch). */
  worktree: boolean;
  /** The channel's roots, which its learn thread shares (the @ picker's). */
  roots?: RootEntry[];
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  /** The learn thread's chat state in the app-level store, restored on
   * opening the view and saved back on closing it. */
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
  onClose: () => void;
}

/**
 * The channel's learn pass, as a pane beside its chat in the Learn view: the
 * proposals to apply or dismiss on top, the hidden learn thread's chat below
 * (to watch the pass, or ask it for changes).
 */
export function LearnPane({ learn, worktree, roots, subscribeChannelEvents, getChatState, onChatStateUnmount, onClose }: LearnPaneProps) {
  const { colors } = useTheme();
  const { open, busy, errors, bulk } = learn;
  const paneRef = useRef<HTMLDivElement>(null);
  useKeepFocus(paneRef);
  const highlighted = useFocusedTurn(paneRef, learn);
  const previewRevision = configPreviewRevision(learn.proposals);

  return (
    <div ref={paneRef} data-testid="learn-pane" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
      {/* Header, like a pane's; docked on a canvas, it drags the pair. */}
      <div
        data-learn-pane-header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "2px 8px",
          height: 22,
          boxSizing: "border-box",
          backgroundColor: colors.surface,
          borderBottom: `1px solid ${colors.border}`,
          fontFamily: fonts.sans,
          flexShrink: 0,
        }}
      >
        <span style={{ padding: "1px 4px", borderRadius: 3, fontSize: 10, fontWeight: 500, color: colors.textLight, backgroundColor: colors.panelLabelBg }}>Learn</span>
        <span style={{ color: colors.textDim, fontSize: 10, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {learn.running ? "reviewing the last run…" : "proposals from the last runs"}
        </span>
        <div style={{ flex: 1 }} />
        {/* Apply all takes the pending proposals, Dismiss all every open
            one; neither starts while either runs. */}
        {open.some((p) => p.status === "pending") && (
          <button data-testid="learn-apply-all" onClick={learn.applyAll} disabled={bulk !== null} style={buttonStyle(colors.active)}>
            Apply all
          </button>
        )}
        {open.length > 0 && (
          <button data-testid="learn-dismiss-all" onClick={learn.dismissAll} disabled={bulk !== null} style={buttonStyle(colors.textDim)}>
            Dismiss all
          </button>
        )}
        <button
          data-testid="learn-close"
          aria-label="Close the Learn view"
          onClick={onClose}
          title="Close the Learn view"
          style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", lineHeight: 1, display: "flex", alignItems: "center", borderRadius: 2 }}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = colors.hoverBg;
            e.currentTarget.style.color = colors.textLight;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = "transparent";
            e.currentTarget.style.color = colors.textDim;
          }}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* A turn's Learn action whose request failed: why, for the turn it
          asked for. */}
      {learn.focus && learn.turnErrors.has(learn.focus.messageId) && (
        <div data-testid="learn-turn-error" style={{ padding: "8px 10px", fontSize: 11, fontFamily: fonts.sans, color: colors.error, borderBottom: `1px solid ${colors.border}`, flexShrink: 0 }}>
          {learn.turnErrors.get(learn.focus.messageId)}
        </div>
      )}
      {learn.proposals.length > 0 && (
        <div data-learn-proposals style={{ maxHeight: "45%", overflowY: "auto", flexShrink: 0, borderBottom: `1px solid ${colors.border}` }}>
          {learn.proposals.map((p) => (
            <ProposalCard
              key={p.id}
              proposal={p}
              open={open.includes(p)}
              highlighted={!!p.message_id && p.message_id === highlighted}
              worktree={worktree}
              previewRevision={previewRevision}
              busy={busy.has(p.id)}
              error={errors.get(p.id)}
              onApply={learn.apply}
              onDismiss={learn.dismiss}
            />
          ))}
        </div>
      )}

      <div data-learn-thread style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <style>{"@keyframes loop-learn-blink { 50% { outline-color: transparent; } }"}</style>
        {learn.learnChannelId ? (
          <LearnThread
            key={learn.learnChannelId}
            learnChannelId={learn.learnChannelId}
            running={learn.running}
            roots={roots}
            subscribeChannelEvents={subscribeChannelEvents}
            getChatState={getChatState}
            onChatStateUnmount={onChatStateUnmount}
          />
        ) : (
          <div style={{ padding: 16, color: colors.textDim, fontFamily: fonts.sans, fontSize: 12 }}>
            No learn pass yet. With learn on in the composer, a hidden forked session reviews the next run here.
          </div>
        )}
      </div>
    </div>
  );
}

// A focused button that goes away (a card's Apply or Dismiss replaced by its
// status once settled; Apply all or Dismiss all once nothing's left for
// them) would drop focus to the page. It moves on instead: to the next open
// card's Apply (the one after the card it was in, else the nearest before),
// else to the pane's close button. One only disabled meanwhile (in flight)
// keeps its place; if it comes back (the request failed), focus does too.
// Focus that left the pane or its buttons on its own (a click or Tab
// elsewhere) isn't followed, nor is focus in the learn thread's chat.
function useKeepFocus(paneRef: React.RefObject<HTMLDivElement | null>) {
  const lastRef = useRef<{ el: HTMLElement; card: string | null } | null>(null);
  useEffect(() => {
    const pane = paneRef.current;
    if (!pane) return;
    const onFocusIn = (e: FocusEvent) => {
      const el = e.target instanceof HTMLElement ? e.target : null;
      lastRef.current = el && pane.contains(el) && !el.closest("[data-learn-thread]") ? { el, card: el.closest<HTMLElement>("[data-proposal-id]")?.dataset.proposalId ?? null } : null;
    };
    // A click moves focus (to what's clicked, or to the page), except on
    // what already has it.
    const onPointerDown = (e: PointerEvent) => {
      const focused = document.activeElement;
      if (!(focused && focused !== document.body && e.target instanceof Node && focused.contains(e.target))) lastRef.current = null;
    };
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("focusin", onFocusIn);
      document.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, [paneRef]);
  // After every render: the settled card or the emptied header re-renders
  // the pane.
  useLayoutEffect(() => {
    const pane = paneRef.current;
    const last = lastRef.current;
    if (!pane || !last) return;
    const active = document.activeElement;
    if (active && active !== document.body && active !== last.el) return;
    if (last.el.isConnected) {
      if (active !== last.el && !(last.el as HTMLButtonElement).disabled) last.el.focus();
      return;
    }
    const cards = [...pane.querySelectorAll<HTMLElement>("[data-proposal-id]")];
    const at = cards.findIndex((c) => c.dataset.proposalId === last.card);
    const order = at < 0 ? cards : [...cards.slice(at + 1), ...cards.slice(0, at).reverse()];
    const next = order.map((c) => c.querySelector<HTMLButtonElement>('[data-testid="learn-apply"]:not(:disabled)')).find((b) => b) ?? pane.querySelector<HTMLElement>('[data-testid="learn-close"]');
    next?.focus();
  });
}

// How long the cards a turn's Learn action asked for stay outlined.
const FOCUS_HIGHLIGHT_MS = 4000;

// The turn a turn's Learn action asked for (learn.focus): once its proposals
// are in the list (a pass still running files them later), the first is
// brought into view and all of them are outlined for a while. Returns the
// outlined turn's message id.
function useFocusedTurn(paneRef: React.RefObject<HTMLDivElement | null>, learn: LearnView): string | null {
  const [highlighted, setHighlighted] = useState<string | null>(null);
  const { focus, proposals } = learn;
  const focusedShown = !!focus && proposals.some((p) => p.message_id === focus.messageId);
  useEffect(() => {
    if (!focus || !focusedShown) return;
    const card = paneRef.current?.querySelector(`[data-learn-proposals] [data-message-id="${CSS.escape(focus.messageId)}"]`);
    card?.scrollIntoView({ behavior: "smooth", block: "start" });
    setHighlighted(focus.messageId);
    const t = setTimeout(() => setHighlighted(null), FOCUS_HIGHLIGHT_MS);
    return () => clearTimeout(t);
  }, [paneRef, focus, focusedShown]);
  return highlighted;
}

function LearnThread({
  learnChannelId,
  running,
  roots,
  subscribeChannelEvents,
  getChatState,
  onChatStateUnmount,
}: {
  learnChannelId: string;
  running: boolean;
  roots?: RootEntry[];
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
}) {
  const subscribe = useCallback((listener: ChatEventListener) => subscribeChannelEvents?.(learnChannelId, listener) ?? (() => {}), [learnChannelId, subscribeChannelEvents]);
  // A reply (or a pass) running when the view opens shows as it is: its
  // Stop button, streamed text, activity and gates. The store keeps them up
  // to date meanwhile, the learn thread staying subscribed (see useLearn).
  const onUnmount = useCallback((state: ActiveChatState) => onChatStateUnmount?.(learnChannelId, state), [learnChannelId, onChatStateUnmount]);
  const chatState = useChatState(learnChannelId, running, { initialState: getChatState?.(learnChannelId), onUnmount, subscribeChatEvents: subscribe });
  return <ChatView channelId={learnChannelId} chatState={chatState} roots={roots} noAutoFocus />;
}

function ProposalCard({
  proposal: p,
  open,
  highlighted,
  worktree,
  previewRevision,
  busy,
  error,
  onApply,
  onDismiss,
}: {
  proposal: LearnProposal;
  /** Still waiting on the user (see LearnView.open). */
  open: boolean;
  /** Asked for by its turn's Learn action: outlined for a while. */
  highlighted: boolean;
  worktree: boolean;
  /** See configPreviewRevision: a change re-fetches the config preview. */
  previewRevision: string;
  busy: boolean;
  /** The last apply or dismiss request failed, with this. */
  error?: string;
  onApply: (id: number) => Promise<void>;
  onDismiss: (id: number) => Promise<void>;
}) {
  const { colors } = useTheme();
  const caveat = proposalCaveat(p, worktree);
  const settled = isSettledProposal(p);
  return (
    <div
      data-testid="learn-proposal"
      data-proposal-id={p.id}
      data-status={p.status}
      data-message-id={p.message_id}
      data-highlighted={highlighted ? "true" : undefined}
      style={{
        padding: "8px 10px",
        outline: highlighted ? `2px solid ${colors.active}` : "none",
        outlineOffset: -2,
        animation: highlighted ? "loop-learn-blink 0.5s ease-in-out 2" : undefined,
        borderBottom: `1px solid ${colors.border}`,
        fontFamily: fonts.sans,
        fontSize: 12,
        color: colors.textLight,
        opacity: settled ? 0.55 : 1,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{ fontFamily: fonts.mono, fontSize: 10, color: colors.textDim, border: `1px solid ${colors.border}`, borderRadius: 8, padding: "0 6px" }}>{learnKindLabel(p.kind)}</span>
        <span style={{ fontWeight: 600, flex: 1, minWidth: 0 }}>{p.title}</span>
        {open ? (
          <>
            <button
              data-testid="learn-apply"
              onClick={() => onApply(p.id)}
              disabled={busy}
              aria-label={`${p.status === "pending" ? "Apply" : "Retry"} “${p.title}”`}
              style={buttonStyle(colors.active)}
            >
              {p.status === "pending" ? "Apply" : "Retry"}
            </button>
            <button data-testid="learn-dismiss" onClick={() => onDismiss(p.id)} disabled={busy} aria-label={`Dismiss “${p.title}”`} style={buttonStyle(colors.textDim)}>
              Dismiss
            </button>
          </>
        ) : (
          <span style={{ fontSize: 11, color: colors.textDim }}>{p.status}</span>
        )}
      </div>
      <div style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.textDim, marginTop: 4, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{proposalDetail(p)}</div>
      {p.rationale && <div style={{ fontSize: 11, color: colors.textDim, marginTop: 4 }}>{p.rationale}</div>}
      {caveat && <div style={{ fontSize: 11, color: colors.warning, marginTop: 4 }}>{caveat}</div>}
      {open && editsProjectConfig(p.kind) && <ConfigPreview id={p.id} status={p.status} revision={previewRevision} />}
      {p.status === "failed" && p.error && <div style={{ fontSize: 11, color: colors.error, marginTop: 4 }}>{p.error}</div>}
      {p.status === "withdrawn" && p.withdrawn_reason && (
        <div data-testid="learn-withdrawn-reason" style={{ fontSize: 11, color: colors.textDim, marginTop: 4 }}>
          Withdrawn by a later learn pass: {p.withdrawn_reason}
        </div>
      )}
      {error && (
        <div data-testid="learn-request-error" style={{ fontSize: 11, color: colors.error, marginTop: 4 }}>
          {error}
        </div>
      )}
    </div>
  );
}

// ConfigPreview shows the edit applying a config-kind proposal would make to
// the project config, worked out by the server the way apply makes it. It's
// fetched again when the proposal's status changes (a failed apply) or
// another proposal edited the config.
function ConfigPreview({ id, status, revision }: { id: number; status: string; revision: string }) {
  const { colors } = useTheme();
  const [preview, setPreview] = useState<LearnProposalPreview | null>(null);
  useEffect(() => {
    let cancelled = false;
    fetchLearnProposalPreview(id)
      .then((p) => {
        if (!cancelled) setPreview(p);
      })
      .catch((e) => {
        if (!cancelled) setPreview(null);
        logErr("previewing learn proposal")(e);
      });
    return () => {
      cancelled = true;
    };
  }, [id, status, revision]);
  if (!preview) return null;
  if (preview.error) {
    return (
      <div data-testid="learn-preview-error" style={{ fontSize: 11, color: colors.warning, marginTop: 4 }}>
        Applying would fail: {preview.error}
      </div>
    );
  }
  if (!preview.path) return null;
  if (!preview.diff) {
    return (
      <div data-testid="learn-preview-unchanged" style={{ fontSize: 11, color: colors.textDim, marginTop: 4 }}>
        Already in {preview.path}; applying changes nothing.
      </div>
    );
  }
  return <UnifiedDiff diff={preview.diff} testId="learn-preview-diff" style={{ fontSize: 11, margin: "6px 0 0", maxHeight: 200 }} />;
}

function buttonStyle(color: string): React.CSSProperties {
  return {
    background: "transparent",
    border: `1px solid ${color}`,
    color,
    cursor: "pointer",
    padding: "2px 8px",
    fontSize: 10,
    fontFamily: fonts.mono,
    lineHeight: 1.4,
    borderRadius: 10,
  };
}
