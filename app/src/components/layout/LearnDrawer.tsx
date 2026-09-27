import { useCallback, useState } from "react";
import type { LearnProposal } from "../../api/learn";
import { useChatState } from "../../hooks/useChatState";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ChatView } from "../chat/ChatView";
import { isOpenProposal, learnKindLabel, proposalCaveat, proposalDetail } from "../chat/learnState";

// Width of the drawer; it never covers more than the layout it slides over.
const DRAWER_WIDTH = 520;

// How long the drawer takes to slide in or out.
export const LEARN_DRAWER_SLIDE_MS = 200;

interface LearnDrawerProps {
  learn: LearnView;
  /** False slides the drawer out past the right edge; the parent unmounts it
   * once the slide ends. */
  shown: boolean;
  /** Slide duration in ms; 0 skips the animation. */
  slideMs: number;
  /** The learning channel is a worktree thread (renames leave its branch). */
  worktree: boolean;
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  onClose: () => void;
}

/**
 * The channel's learn pass, full height over the right of the layout: the
 * proposals to apply or dismiss on top, the hidden learn thread's chat below
 * (to watch the pass, or ask it for changes).
 */
export function LearnDrawer({ learn, shown, slideMs, worktree, subscribeChannelEvents, onClose }: LearnDrawerProps) {
  const { colors } = useTheme();
  const open = learn.proposals.filter(isOpenProposal);
  // Proposals with an apply or dismiss in flight; their buttons are disabled
  // so a second click can't race the first.
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set());
  const [applyingAll, setApplyingAll] = useState(false);

  const settle = useCallback(async (id: number, action: (id: number) => Promise<void>) => {
    setBusy((cur) => new Set(cur).add(id));
    try {
      await action(id);
    } finally {
      setBusy((cur) => {
        const next = new Set(cur);
        next.delete(id);
        return next;
      });
    }
  }, []);
  const apply = useCallback((id: number) => settle(id, learn.apply), [settle, learn.apply]);
  const dismiss = useCallback((id: number) => settle(id, learn.dismiss), [settle, learn.dismiss]);

  const applyAll = useCallback(async () => {
    setApplyingAll(true);
    for (const p of learn.proposals.filter((x) => x.status === "pending" && !busy.has(x.id))) {
      await apply(p.id);
    }
    setApplyingAll(false);
  }, [learn.proposals, busy, apply]);

  return (
    <div
      data-testid="learn-drawer"
      style={{
        position: "absolute",
        top: 0,
        right: 0,
        bottom: 0,
        width: DRAWER_WIDTH,
        maxWidth: "100%",
        zIndex: 20,
        display: "flex",
        flexDirection: "column",
        background: colors.bg,
        borderLeft: `1px solid ${colors.border}`,
        boxShadow: shown ? `-4px 0 12px ${colors.shadow}` : "none",
        transform: shown ? "translateX(0)" : "translateX(100%)",
        transition: slideMs ? `transform ${slideMs}ms ${shown ? "ease-out" : "ease-in"}, box-shadow ${slideMs}ms` : "none",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "6px 10px",
          borderBottom: `1px solid ${colors.border}`,
          fontFamily: fonts.sans,
          fontSize: 12,
          color: colors.textLight,
          flexShrink: 0,
        }}
      >
        <span style={{ fontWeight: 600 }}>Learn</span>
        <span style={{ color: colors.textDim, fontSize: 11 }}>{learn.running ? "reviewing the last run…" : "proposals from the last runs"}</span>
        <div style={{ flex: 1 }} />
        {open.some((p) => p.status === "pending") && (
          <button data-testid="learn-apply-all" onClick={applyAll} disabled={applyingAll} style={buttonStyle(colors.active)}>
            Apply all
          </button>
        )}
        <button data-testid="learn-drawer-close" onClick={onClose} title="Close" style={{ ...buttonStyle(colors.textDim), border: "none", fontSize: 14 }}>
          &times;
        </button>
      </div>

      {learn.proposals.length > 0 && (
        <div style={{ maxHeight: "45%", overflowY: "auto", flexShrink: 0, borderBottom: `1px solid ${colors.border}` }}>
          {learn.proposals.map((p) => (
            <ProposalCard key={p.id} proposal={p} worktree={worktree} busy={busy.has(p.id)} onApply={apply} onDismiss={dismiss} />
          ))}
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        {learn.learnChannelId ? (
          <LearnThread learnChannelId={learn.learnChannelId} running={learn.running} subscribeChannelEvents={subscribeChannelEvents} />
        ) : (
          <div style={{ padding: 16, color: colors.textDim, fontFamily: fonts.sans, fontSize: 12 }}>
            No learn pass yet. Turn on learn in the composer; after the next run, a hidden forked session reviews it here.
          </div>
        )}
      </div>
    </div>
  );
}

function LearnThread({
  learnChannelId,
  running,
  subscribeChannelEvents,
}: {
  learnChannelId: string;
  running: boolean;
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
}) {
  const subscribe = useCallback((listener: ChatEventListener) => subscribeChannelEvents?.(learnChannelId, listener) ?? (() => {}), [learnChannelId, subscribeChannelEvents]);
  const chatState = useChatState(learnChannelId, running, { subscribeChatEvents: subscribe });
  return <ChatView key={learnChannelId} channelId={learnChannelId} chatState={chatState} hideLearn />;
}

function ProposalCard({
  proposal: p,
  worktree,
  busy,
  onApply,
  onDismiss,
}: {
  proposal: LearnProposal;
  worktree: boolean;
  busy: boolean;
  onApply: (id: number) => Promise<void>;
  onDismiss: (id: number) => Promise<void>;
}) {
  const { colors } = useTheme();
  const caveat = proposalCaveat(p, worktree);
  const settled = p.status === "applied" || p.status === "dismissed";
  return (
    <div
      data-testid="learn-proposal"
      data-status={p.status}
      style={{
        padding: "8px 10px",
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
        {isOpenProposal(p) ? (
          <>
            <button data-testid="learn-apply" onClick={() => onApply(p.id)} disabled={busy} style={buttonStyle(colors.active)}>
              {p.status === "pending" ? "Apply" : "Retry"}
            </button>
            <button data-testid="learn-dismiss" onClick={() => onDismiss(p.id)} disabled={busy} style={buttonStyle(colors.textDim)}>
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
      {p.status === "failed" && p.error && <div style={{ fontSize: 11, color: colors.error, marginTop: 4 }}>{p.error}</div>}
    </div>
  );
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
