import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import {
  deleteReviewSession,
  getReviewSession,
  loadReviewPR,
  parsePRInput,
  pushAllReviewComments,
  pushReviewComment,
  runReview,
  type ReviewComment,
  type ReviewSession,
  type ReviewStatus,
} from "../../api/review";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { WSEvent } from "../../types";

interface ReviewPanelProps {
  channelId: string;
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
  onClose?: () => void;
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

export function ReviewPanel({ channelId, subscribeChatEvents, onClose }: ReviewPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const [prInput, setPrInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Initial fetch on channel change.
  useEffect(() => {
    let cancelled = false;
    setSession(null);
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

  const onLoad = useCallback(async () => {
    const num = parsePRInput(prInput);
    if (num === null) { setError("Enter a PR number or URL"); return; }
    setBusy(true); setError(null);
    try {
      const resp = await loadReviewPR(channelId, num);
      if (resp.present && resp.session) setSession(resp.session);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId, prInput]);

  const onRun = useCallback(async () => {
    setBusy(true); setError(null);
    try {
      await runReview(channelId);
      // Status will transition via review.status WS event; no need to refetch.
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
      setPrInput("");
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

  const inputBarStyle: React.CSSProperties = {
    flex: 1,
    background: colors.surface,
    color: colors.text,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    padding: "4px 8px",
    fontSize: 12,
    fontFamily: fonts.sans,
  };

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
      {/* Header / PR loader */}
      <div
        style={{
          display: "flex",
          gap: 6,
          padding: "6px 8px",
          borderBottom: `1px solid ${colors.border}`,
          alignItems: "center",
        }}
      >
        {!session || session.status === "idle" || session.status === "error" ? (
          <>
            <input
              data-testid="review-pr-input"
              value={prInput}
              onChange={(e) => setPrInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && !busy) void onLoad(); }}
              placeholder="PR number or URL"
              disabled={busy}
              style={inputBarStyle}
            />
            <button
              data-testid="review-load-btn"
              onClick={() => void onLoad()}
              disabled={busy || prInput.trim() === ""}
              style={primaryBtnStyle}
            >
              Load
            </button>
          </>
        ) : (
          <>
            <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, color: colors.text }}>
                <span style={{ fontFamily: "monospace" }}>#{session.pr?.number}</span>
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {session.pr?.title ?? ""}
                </span>
              </div>
              <div style={{ fontSize: 10, color: colors.textDim, fontFamily: fonts.sans }}>
                {session.pr?.base_ref} ← {session.pr?.head_ref} · {statusLabel(session.status)}
              </div>
            </div>
            <button
              data-testid="review-run-btn"
              onClick={() => void onRun()}
              disabled={busy || session.status !== "ready"}
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
        {onClose && (
          <button
            onClick={onClose}
            style={btnStyle}
            title="Close panel"
          >
            ×
          </button>
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
        {!session && (
          <EmptyHint colors={colors} />
        )}
        {session && session.comments.length === 0 && session.status !== "reviewing" && (
          <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
            {session.status === "ready"
              ? "No comments yet. Click Run to start the agent review."
              : `Status: ${statusLabel(session.status)}`}
          </div>
        )}
        {session && session.status === "reviewing" && session.comments.length === 0 && (
          <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
            Reviewing... comments will appear as the agent emits them.
          </div>
        )}
        {session && session.comments.map((c) => (
          <CommentRow key={c.id} comment={c} colors={colors} onPush={onPushOne} />
        ))}
      </div>
    </div>
  );
}

function EmptyHint({ colors }: { colors: ReturnType<typeof useTheme>["colors"] }) {
  return (
    <div style={{ padding: 24, color: colors.textDim, fontSize: 12, textAlign: "center", lineHeight: 1.6 }}>
      Enter a GitHub PR number or URL to load its diff into a local worktree,
      then run an agent review to generate inline comments you can push back
      to the PR.
    </div>
  );
}

function CommentRow({
  comment,
  colors,
  onPush,
}: {
  comment: ReviewComment;
  colors: ReturnType<typeof useTheme>["colors"];
  onPush: (c: ReviewComment) => void | Promise<void>;
}) {
  return (
    <div
      data-testid={`review-comment-${comment.id}`}
      style={{
        padding: "8px 10px",
        borderBottom: `1px solid ${colors.border}`,
        display: "flex",
        flexDirection: "column",
        gap: 4,
        background: comment.pushed ? "transparent" : colors.surface,
      }}
    >
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 6 }}>
        <span style={{ fontFamily: "monospace", fontSize: 11, color: colors.text, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {comment.path}:{comment.line}
        </span>
        <div style={{ display: "flex", gap: 4, alignItems: "center" }}>
          {comment.pushed ? (
            <span style={{ fontSize: 10, color: colors.textDim }}>pushed</span>
          ) : (
            <button
              data-testid={`review-comment-push-${comment.id}`}
              onClick={() => void onPush(comment)}
              style={{
                background: "transparent",
                color: colors.text,
                border: `1px solid ${colors.border}`,
                borderRadius: 3,
                padding: "1px 6px",
                fontSize: 10,
                fontFamily: fonts.sans,
                cursor: "pointer",
              }}
              title="Push this comment to the PR"
            >
              Push
            </button>
          )}
        </div>
      </div>
      <div style={{ fontSize: 12, color: colors.text, whiteSpace: "pre-wrap" }}>
        {comment.body}
      </div>
    </div>
  );
}
