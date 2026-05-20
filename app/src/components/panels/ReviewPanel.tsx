import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import {
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
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { WSEvent } from "../../types";
import { ReviewDiffView } from "./ReviewDiffView";

interface ReviewPanelProps {
  channelId: string;
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
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

export function ReviewPanel({ channelId, subscribeChatEvents }: ReviewPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const [prList, setPrList] = useState<ReviewPR[] | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadingPR, setLoadingPR] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const hasSession = session !== null && session.status !== "idle" && session.status !== "error";

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
      }
    };
    return subscribeChatEvents(listener);
  }, [channelId, subscribeChatEvents]);

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

  const onPushAll = useCallback(async () => {
    setBusy(true); setError(null);
    try {
      const result = await pushAllReviewComments(channelId);
      setSession((prev) => prev ? {
        ...prev,
        comments: prev.comments.map((x) => x.pushed ? x : { ...x, pushed: true }),
      } : prev);
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
                {session?.pr?.base_ref} ← {session?.pr?.head_ref} · {session ? statusLabel(session.status) : ""}
              </div>
            </div>
            <button
              data-testid="review-sync-btn"
              onClick={() => void onSync()}
              disabled={busy || session?.status === "reviewing" || session?.status === "loading"}
              style={btnStyle}
              title="Pull the latest PR head, diff, and GitHub comments"
            >
              Sync
            </button>
            <button
              data-testid="review-run-btn"
              onClick={() => void onRun()}
              disabled={busy || session?.status !== "ready"}
              style={primaryBtnStyle}
              title="Run agent review"
            >
              Run
            </button>
            {pendingCount > 0 && (
              <button
                data-testid="review-push-all-btn"
                onClick={() => void onPushAll()}
                disabled={busy}
                style={btnStyle}
                title="Push all unpushed comments"
              >
                Push all ({pendingCount})
              </button>
            )}
            <button
              data-testid="review-close-btn"
              onClick={() => void onCloseSession()}
              disabled={busy}
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
            rawDiff={session.raw_diff ?? ""}
            comments={session.comments}
            onPushComment={onPushOne}
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

