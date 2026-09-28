import { useCallback, useState } from "react";
import type { LearnProposal } from "../../api/learn";
import { useChatState } from "../../hooks/useChatState";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ChatView } from "../chat/ChatView";
import { isOpenProposal, learnKindLabel, proposalCaveat, proposalDetail } from "../chat/learnState";

interface LearnPaneProps {
  learn: LearnView;
  /** The learning channel is a worktree thread (renames leave its branch). */
  worktree: boolean;
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  onClose: () => void;
}

/**
 * The channel's learn pass, as a pane beside its chat in the Learn view: the
 * proposals to apply or dismiss on top, the hidden learn thread's chat below
 * (to watch the pass, or ask it for changes).
 */
export function LearnPane({ learn, worktree, subscribeChannelEvents, onClose }: LearnPaneProps) {
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
    <div data-testid="learn-pane" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
      {/* Header, like a pane's */}
      <div
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
        {open.some((p) => p.status === "pending") && (
          <button data-testid="learn-apply-all" onClick={applyAll} disabled={applyingAll} style={buttonStyle(colors.active)}>
            Apply all
          </button>
        )}
        <button
          data-testid="learn-close"
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
