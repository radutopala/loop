import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import {
  deleteReviewComment,
  deleteReviewSession,
  getReviewSession,
  listReviewPRs,
  loadReviewPR,
  pushAllReviewComments,
  pushReviewComment,
  runReview,
  syncReviewSession,
  type ReviewComment,
  type ReviewPR,
  type ReviewSession,
  type ReviewStatus,
} from "../../api/review";
import { sendMessage } from "../../api/channels";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { WSEvent } from "../../types";
import { ReviewDiffView } from "./ReviewDiffView";

// Build the agent prompt for a single review comment. The agent runs
// inside the channel's worktree, so it has direct filesystem access —
// we only need to hand it enough metadata to locate the comment in the
// PR's diff and the original review note verbatim.
function buildSinglePromptForChat(c: ReviewComment, headSHA?: string, prNumber?: number): string {
  const sideLabel = c.side === "LEFT" ? "deleted/old" : "added/new";
  const lines: string[] = [];
  lines.push(`Please address this review comment from the PR:`);
  lines.push("");
  lines.push(`- File: \`${c.path}\``);
  lines.push(`- Line: ${c.line} (${c.side || "RIGHT"} — ${sideLabel})`);
  if (prNumber) lines.push(`- PR: #${prNumber}`);
  if (headSHA) lines.push(`- Commit: ${headSHA}`);
  if (c.author) lines.push(`- Author: @${c.author}`);
  lines.push("");
  lines.push(`Comment:`);
  lines.push("");
  for (const ln of c.body.split("\n")) lines.push(`> ${ln}`);
  return lines.join("\n");
}

// Build a single prompt that batches multiple comments for "Push all to
// chat". Each comment renders the same metadata block as the single
// version so the agent can act on them independently without losing
// context.
function buildBatchPromptForChat(cs: ReviewComment[], headSHA?: string, prNumber?: number): string {
  const header: string[] = [];
  header.push(`Please address the following ${cs.length} review comment${cs.length === 1 ? "" : "s"} from the PR:`);
  if (prNumber || headSHA) header.push("");
  if (prNumber) header.push(`- PR: #${prNumber}`);
  if (headSHA) header.push(`- Commit: ${headSHA}`);
  const blocks = cs.map((c, i) => {
    const sideLabel = c.side === "LEFT" ? "deleted/old" : "added/new";
    const b: string[] = [];
    b.push("");
    b.push(`---`);
    b.push(`### ${i + 1}. \`${c.path}\`:${c.line} (${c.side || "RIGHT"} — ${sideLabel})${c.author ? ` — @${c.author}` : ""}`);
    b.push("");
    for (const ln of c.body.split("\n")) b.push(`> ${ln}`);
    return b.join("\n");
  });
  return [...header, ...blocks].join("\n");
}

interface ReviewPanelProps {
  channelId: string;
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
  /** Dismiss the sidebar's `rev` pill — fired once the user is looking at the review. */
  clearReviewPill?: (channelId: string) => void;
}

function statusLabel(status: ReviewStatus): string {
  switch (status) {
    case "idle": return "Idle";
    case "loading": return "Loading PR...";
    case "ready": return "Ready";
    case "reviewing": return "Reviewing...";
    case "error": return "Error";
  }
}

export function ReviewPanel({ channelId, subscribeChatEvents, clearReviewPill }: ReviewPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const [prList, setPrList] = useState<ReviewPR[] | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadingPR, setLoadingPR] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const hasSession = session !== null && session.status !== "idle" && session.status !== "error";

  // The `rev` pill in the sidebar is a "go look" badge — once this panel
  // is mounted and showing a session, the user has acknowledged it.
  // Drop the pill on every render where the session is visualized so a
  // WS reconnect that re-runs rehydrateReviewSessions (and re-adds the
  // pill) can't briefly relight it while the panel is in view.
  useEffect(() => {
    if (hasSession) clearReviewPill?.(channelId);
  }, [hasSession, channelId, clearReviewPill]);

  // Initial fetch on channel change.
  useEffect(() => {
    let cancelled = false;
    setSession(null);
    setPrList(null);
    setError(null);
    (async () => {
      try {
        const resp = await getReviewSession(channelId);
        if (cancelled) return;
        if (resp.present && resp.session) setSession(resp.session);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => { cancelled = true; };
  }, [channelId]);

  // Fetch open PRs when there's no active session — i.e. show the picker.
  useEffect(() => {
    if (hasSession) return;
    let cancelled = false;
    setListLoading(true);
    (async () => {
      try {
        const prs = await listReviewPRs(channelId);
        if (!cancelled) setPrList(prs);
      } catch (e) {
        if (!cancelled) {
          setPrList([]);
          setError(e instanceof Error ? e.message : String(e));
        }
      } finally {
        if (!cancelled) setListLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [channelId, hasSession]);

  // WS subscription: pick up review.comment + review.status for this channel.
  useEffect(() => {
    if (!subscribeChatEvents) return;
    const listener: ChatEventListener = (event: WSEvent) => {
      if (event.channel_id !== channelId) return;
      if (event.type === "review.comment") {
        const c = event.data as ReviewComment;
        setSession((prev) => {
          if (!prev) return prev;
          if (prev.comments.some((x) => x.id === c.id)) return prev;
          return { ...prev, comments: [...prev.comments, c] };
        });
      } else if (event.type === "review.status") {
        const d = event.data as { status: ReviewStatus; error?: string };
        setSession((prev) => prev ? { ...prev, status: d.status, error: d.error ?? "" } : prev);
        // Suppress the sidebar pill on every fresh "ready" — the panel
        // is mounted and the user is already looking at the review, so
        // a new pill would be noise. Without this, a re-run that lands
        // back in ready while the panel is open would relight the pill.
        if (d.status === "ready") clearReviewPill?.(channelId);
      } else if (event.type === "review.diff") {
        // Backend re-rendered the diff with widened context after an
        // agent comment landed outside the current hunks. Swap the
        // raw_diff in place — the diff view re-parses on the new value
        // and existing comments re-bind to the wider hunk set.
        const d = event.data as { raw_diff: string };
        setSession((prev) => prev ? { ...prev, raw_diff: d.raw_diff } : prev);
      }
    };
    return subscribeChatEvents(listener);
  }, [channelId, subscribeChatEvents, clearReviewPill]);

  const onSelectPR = useCallback(async (pr: ReviewPR) => {
    setBusy(true); setError(null); setLoadingPR(pr.number);
    try {
      const resp = await loadReviewPR(channelId, pr.number);
      if (resp.present && resp.session) setSession(resp.session);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false); setLoadingPR(null);
    }
  }, [channelId]);

  const onRun = useCallback(async () => {
    setBusy(true); setError(null);
    // Optimistically flip to "reviewing" so the Run button stays disabled
    // even before the review.status WS event lands — otherwise there's a
    // brief window after setBusy(false) where status is still "ready" and
    // the button re-enables itself.
    setSession((prev) => prev ? { ...prev, status: "reviewing" } : prev);
    try {
      await runReview(channelId);
      // Status will transition via review.status WS event; no need to refetch.
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSession((prev) => prev ? { ...prev, status: "ready" } : prev);
    } finally {
      setBusy(false);
    }
  }, [channelId]);

  const onSync = useCallback(async () => {
    setBusy(true); setError(null);
    try {
      const resp = await syncReviewSession(channelId);
      if (resp.present && resp.session) setSession(resp.session);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId]);

  const onCloseSession = useCallback(async () => {
    setBusy(true); setError(null);
    try {
      await deleteReviewSession(channelId);
      setSession(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId]);

  const onPushOne = useCallback(async (c: ReviewComment) => {
    setError(null);
    try {
      await pushReviewComment(channelId, c.id);
      setSession((prev) => prev ? {
        ...prev,
        comments: prev.comments.map((x) => x.id === c.id ? { ...x, pushed: true } : x),
      } : prev);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [channelId]);

  const onDeleteOne = useCallback(async (c: ReviewComment) => {
    // Confirm in-line: deleting a pushed/github comment removes it from
    // the PR on GitHub too, which is irreversible — but for unpushed
    // local agent comments it's just dropping a draft, so the prompt
    // shouldn't be alarmist. Keep one prompt with a wording that adapts
    // to the situation.
    const hitsGitHub = !!c.github_id;
    const msg = hitsGitHub
      ? "Delete this comment from GitHub? This cannot be undone."
      : "Discard this comment?";
    if (!window.confirm(msg)) return;
    setError(null);
    try {
      await deleteReviewComment(channelId, c.id);
      setSession((prev) => prev ? {
        ...prev,
        comments: prev.comments.filter((x) => x.id !== c.id),
      } : prev);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [channelId]);

  // Ask WorkspaceLayout to ensure a chat panel is mounted in the active
  // layout. The listener is a no-op if one already exists; otherwise it
  // anchors the new chat panel to the right of this review panel so the
  // user sees the prompt land beside the diff they pushed it from.
  const ensureChatOpen = useCallback(() => {
    window.dispatchEvent(
      new CustomEvent("loop:open-panel", {
        detail: { channelId, panel: "chat", anchorPanel: "review" },
      }),
    );
  }, [channelId]);

  const onPushOneToChat = useCallback(async (c: ReviewComment) => {
    setError(null);
    try {
      ensureChatOpen();
      const prompt = buildSinglePromptForChat(c, session?.head_sha, session?.pr?.number);
      await sendMessage(channelId, prompt);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [channelId, ensureChatOpen, session?.head_sha, session?.pr?.number]);

  const onPushAllToChat = useCallback(async () => {
    setError(null);
    const pending = (session?.comments ?? []).filter((c) => !c.pushed);
    if (pending.length === 0) return;
    try {
      ensureChatOpen();
      const prompt = buildBatchPromptForChat(pending, session?.head_sha, session?.pr?.number);
      await sendMessage(channelId, prompt);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [channelId, ensureChatOpen, session?.comments, session?.head_sha, session?.pr?.number]);

  const onPushAll = useCallback(async () => {
    setBusy(true); setError(null);
    try {
      const result = await pushAllReviewComments(channelId);
      // The backend's PushAllResult only reports aggregate counts, not
      // per-id outcomes — and a partial failure leaves some comments
      // still pending server-side. Refetch the session so the FE
      // reflects the authoritative pushed/unpushed split and the user
      // can retry just the ones that actually failed.
      const resp = await getReviewSession(channelId);
      if (resp.present && resp.session) setSession(resp.session);
      if (result.failed > 0) {
        setError(`${result.failed} comment(s) failed to push: ${(result.errors ?? []).join("; ")}`);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId]);

  const pendingCount = useMemo(
    () => (session?.comments ?? []).filter((c) => !c.pushed).length,
    [session],
  );

  const btnStyle: React.CSSProperties = {
    background: "transparent",
    color: colors.textDim,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    padding: "4px 10px",
    fontSize: 11,
    fontFamily: fonts.sans,
    cursor: "pointer",
  };

  const primaryBtnStyle: React.CSSProperties = {
    ...btnStyle,
    color: colors.text,
    borderColor: colors.active,
  };

  // Disabled styling is applied inline so the button visibly dims and
  // shows a not-allowed cursor — otherwise `disabled` is a no-op visual.
  const disabledStyle: React.CSSProperties = { opacity: 0.4, cursor: "not-allowed" };
  const syncDisabled = busy || session?.status === "reviewing" || session?.status === "loading";
  const runDisabled = busy || session?.status !== "ready";
  const closeDisabled = busy;
  const pushAllDisabled = busy;

  return (
    <div
      data-testid="review-panel"
      style={{
        display: "flex",
        flexDirection: "column",
        flex: 1,
        height: "100%",
        overflow: "hidden",
        zoom: fontSizes.panels / 12,
        backgroundColor: colors.bg,
      }}
    >
      {/* Header */}
      <div
        style={{
          display: "flex",
          gap: 6,
          padding: "6px 8px",
          borderBottom: `1px solid ${colors.border}`,
          alignItems: "center",
        }}
      >
        {!hasSession ? (
          <div style={{ flex: 1, fontSize: 12, color: colors.textDim, fontFamily: fonts.sans }}>
            Select a PR to review
          </div>
        ) : (
          <>
            <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, color: colors.text, minWidth: 0 }}>
                {session?.pr?.url ? (
                  <a
                    href={session.pr.url}
                    target="_blank"
                    rel="noreferrer noopener"
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      color: colors.active,
                      textDecoration: "none",
                      minWidth: 0,
                    }}
                    title={session.pr.url}
                  >
                    <span style={{ fontFamily: "monospace" }}>#{session.pr.number}</span>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {session.pr.title ?? ""}
                    </span>
                  </a>
                ) : (
                  <>
                    <span style={{ fontFamily: "monospace" }}>#{session?.pr?.number}</span>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {session?.pr?.title ?? ""}
                    </span>
                  </>
                )}
              </div>
              <div style={{ fontSize: 10, color: colors.textDim, fontFamily: fonts.sans }}>
                {session?.pr?.base_ref} ← {session?.pr?.head_ref}
                {session?.head_sha && (
                  <>
                    {" @ "}
                    <span style={{ fontFamily: "monospace" }} title={session.head_sha}>
                      {session.head_sha.slice(0, 7)}
                    </span>
                  </>
                )}
                {" · "}{session ? statusLabel(session.status) : ""}
              </div>
            </div>
            <button
              data-testid="review-sync-btn"
              onClick={() => void onSync()}
              disabled={syncDisabled}
              style={syncDisabled ? { ...btnStyle, ...disabledStyle } : btnStyle}
              title="Pull the latest PR head, diff, and GitHub comments"
            >
              Sync
            </button>
            <button
              data-testid="review-run-btn"
              onClick={() => void onRun()}
              disabled={runDisabled}
              style={runDisabled ? { ...primaryBtnStyle, ...disabledStyle } : primaryBtnStyle}
              title={
                session?.status === "reviewing"
                  ? "Review already running"
                  : "Run agent review"
              }
            >
              {session?.status === "reviewing" ? "Running..." : "Run"}
            </button>
            {pendingCount > 0 && (
              <>
                <button
                  data-testid="review-push-all-chat-btn"
                  onClick={() => void onPushAllToChat()}
                  disabled={busy}
                  style={busy ? { ...btnStyle, ...disabledStyle } : btnStyle}
                  title="Send all unpushed comments to the chat as a single agent prompt"
                >
                  Push all to chat ({pendingCount})
                </button>
                <button
                  data-testid="review-push-all-btn"
                  onClick={() => void onPushAll()}
                  disabled={pushAllDisabled}
                  style={pushAllDisabled ? { ...btnStyle, ...disabledStyle } : btnStyle}
                  title="Push all unpushed comments to GitHub"
                >
                  Push all to GitHub ({pendingCount})
                </button>
              </>
            )}
            <button
              data-testid="review-close-btn"
              onClick={() => void onCloseSession()}
              disabled={closeDisabled}
              style={btnStyle}
              title="Close review session and remove worktree"
            >
              Close
            </button>
          </>
        )}
      </div>

      {/* Error banner */}
      {(error || session?.error) && (
        <div
          data-testid="review-error"
          style={{
            padding: "4px 8px",
            background: colors.dangerBg,
            color: colors.dangerText,
            fontSize: 11,
            borderBottom: `1px solid ${colors.border}`,
          }}
        >
          {error || session?.error}
        </div>
      )}

      {/* Body */}
      <div style={{ flex: 1, overflow: "auto", display: "flex", flexDirection: "column" }}>
        {!hasSession && (
          <PRListPicker
            prs={prList}
            loading={listLoading}
            loadingPR={loadingPR}
            disabled={busy}
            colors={colors}
            onSelect={onSelectPR}
          />
        )}
        {hasSession && session && session.status === "reviewing" && session.comments.length === 0 && (
          <div style={{ padding: "6px 12px", color: colors.textDim, fontSize: 11, borderBottom: `1px solid ${colors.border}` }}>
            Reviewing... comments will appear inline as the agent emits them.
          </div>
        )}
        {hasSession && session && (
          <ReviewDiffView
            channelId={channelId}
            rawDiff={session.raw_diff ?? ""}
            comments={session.comments}
            worktreePath={session.worktree_path}
            onPushComment={onPushOne}
            onPushCommentToChat={onPushOneToChat}
            onDeleteComment={onDeleteOne}
          />
        )}
      </div>
    </div>
  );
}

function PRListPicker({
  prs,
  loading,
  loadingPR,
  disabled,
  colors,
  onSelect,
}: {
  prs: ReviewPR[] | null;
  loading: boolean;
  loadingPR: number | null;
  disabled: boolean;
  colors: ReturnType<typeof useTheme>["colors"];
  onSelect: (pr: ReviewPR) => void | Promise<void>;
}) {
  if (loading && prs === null) {
    return (
      <div data-testid="review-pr-list-loading" style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
        Loading open PRs...
      </div>
    );
  }
  if (prs !== null && prs.length === 0) {
    return (
      <div data-testid="review-pr-list-empty" style={{ padding: 24, color: colors.textDim, fontSize: 12, textAlign: "center", lineHeight: 1.6 }}>
        No open pull requests found in this repo.
      </div>
    );
  }
  if (prs === null) return null;
  return (
    <div data-testid="review-pr-list">
      {prs.map((pr) => (
        <button
          key={pr.number}
          data-testid={`review-pr-row-${pr.number}`}
          onClick={() => void onSelect(pr)}
          disabled={disabled}
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "flex-start",
            gap: 2,
            width: "100%",
            textAlign: "left",
            padding: "8px 10px",
            background: "transparent",
            color: colors.text,
            border: "none",
            borderBottom: `1px solid ${colors.border}`,
            cursor: disabled ? "default" : "pointer",
            fontFamily: fonts.sans,
            opacity: disabled && loadingPR !== pr.number ? 0.5 : 1,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, width: "100%", minWidth: 0 }}>
            <span style={{ fontFamily: "monospace", color: colors.textDim }}>#{pr.number}</span>
            {pr.is_draft && (
              <span
                style={{
                  fontSize: 9,
                  padding: "0 4px",
                  borderRadius: 3,
                  border: `1px solid ${colors.border}`,
                  color: colors.textDim,
                  textTransform: "uppercase",
                }}
              >
                draft
              </span>
            )}
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>
              {pr.title ?? ""}
            </span>
            {loadingPR === pr.number && (
              <span style={{ fontSize: 10, color: colors.textDim }}>loading...</span>
            )}
          </div>
          <div style={{ fontSize: 10, color: colors.textDim }}>
            {pr.base_ref} ← {pr.head_ref}
          </div>
        </button>
      ))}
    </div>
  );
}

