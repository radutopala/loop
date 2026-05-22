import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  syncReviewSession,
  type ReviewComment,
  type ReviewPR,
  type ReviewSession,
  type ReviewStatus,
} from "../../api/review";
import { sendMessage } from "../../api/channels";
import { startWorkflowRun } from "../../api/workflows";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { GateApprovalRequestedData, WSEvent } from "../../types";
import { ApprovalCard } from "../chat/ApprovalCard";
import { ContextMenu } from "../shared/ContextMenu";
import { ReviewDiffView } from "./ReviewDiffView";

type ReviewMode = "review-only" | "review-fix";

const REVIEW_MODE_STORAGE_KEY = "loop.review.lastMode";
const REVIEW_MAX_ITER_STORAGE_KEY = "loop.review.maxIter";

const REVIEW_LOOP_WORKFLOW = "review-loop";
const REVIEW_FIX_LOOP_WORKFLOW = "review-fix-loop";

function readStoredMode(): ReviewMode {
  if (typeof window === "undefined") return "review-only";
  const v = window.localStorage.getItem(REVIEW_MODE_STORAGE_KEY);
  return v === "review-fix" ? "review-fix" : "review-only";
}

function readStoredMaxIter(): number {
  if (typeof window === "undefined") return 3;
  const raw = window.localStorage.getItem(REVIEW_MAX_ITER_STORAGE_KEY);
  const n = raw === null ? NaN : parseInt(raw, 10);
  if (!Number.isFinite(n)) return 3;
  if (n < 1) return 1;
  if (n > 10) return 10;
  return n;
}

function modePrimaryLabel(mode: ReviewMode): string {
  return mode === "review-fix" ? "Run review + fix" : "Run review";
}

// The agent runs inside the channel's worktree, so it has direct
// filesystem access — we only hand it enough metadata to locate the
// comment in the PR's diff and the original review note verbatim.
function sideLabel(side?: string): string {
  return side === "LEFT" ? "deleted/old" : "added/new";
}

function buildSinglePromptForChat(c: ReviewComment, headSHA?: string, prNumber?: number): string {
  const lines: string[] = [];
  lines.push(`Please address this review comment from the PR:`);
  lines.push("");
  lines.push(`- File: \`${c.path}\``);
  lines.push(`- Line: ${c.line} (${c.side || "RIGHT"} — ${sideLabel(c.side)})`);
  if (prNumber) lines.push(`- PR: #${prNumber}`);
  if (headSHA) lines.push(`- Commit: ${headSHA}`);
  if (c.author) lines.push(`- Author: @${c.author}`);
  lines.push("");
  lines.push(`Comment:`);
  lines.push("");
  for (const ln of c.body.split("\n")) lines.push(`> ${ln}`);
  return lines.join("\n");
}

function buildBatchPromptForChat(cs: ReviewComment[], headSHA?: string, prNumber?: number): string {
  const header: string[] = [];
  header.push(`Please address the following ${cs.length} review comment${cs.length === 1 ? "" : "s"} from the PR:`);
  if (prNumber || headSHA) header.push("");
  if (prNumber) header.push(`- PR: #${prNumber}`);
  if (headSHA) header.push(`- Commit: ${headSHA}`);
  const blocks = cs.map((c, i) => {
    const b: string[] = [];
    b.push("");
    b.push(`---`);
    b.push(`### ${i + 1}. \`${c.path}\`:${c.line} (${c.side || "RIGHT"} — ${sideLabel(c.side)})${c.author ? ` — @${c.author}` : ""}`);
    b.push("");
    for (const ln of c.body.split("\n")) b.push(`> ${ln}`);
    return b.join("\n");
  });
  return [...header, ...blocks].join("\n");
}

interface ReviewPanelProps {
  channelId: string;
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
  /**
   * Register this channel as having a Review panel mounted. The store
   * drops the pill on call and suppresses re-lights (from the WS event
   * handler or the WS-reconnect rehydrate path) until the returned
   * deregister fn is called on unmount.
   */
  registerReviewView?: (channelId: string) => () => void;
  /**
   * True when a chat panel is mounted in the active layout. When false the
   * Review panel renders gate approval prompts inline (otherwise the chat
   * panel handles them and the panel only shows a "see chat" chip).
   */
  hasChatPanel?: boolean;
  /** Per-source pending gate approvals on this channel (keyed by source).
   *  Pass through `chatState.gateApprovals` from the chat store. */
  gateApprovals?: Record<string, GateApprovalRequestedData>;
  /** Drop a resolved gate from the store. Called when the inline
   *  ApprovalCard fires its `onResolved`. */
  onClearGateApproval?: (source: string) => void;
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

export function ReviewPanel({
  channelId,
  subscribeChatEvents,
  registerReviewView,
  hasChatPanel,
  gateApprovals,
  onClearGateApproval,
}: ReviewPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const [prList, setPrList] = useState<ReviewPR[] | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadingPR, setLoadingPR] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<ReviewMode>(() => readStoredMode());
  const [maxIter, setMaxIter] = useState<number>(() => readStoredMaxIter());
  const [loopRunId, setLoopRunId] = useState<string | null>(null);
  const [loopChip, setLoopChip] = useState<string>("");
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const caretRef = useRef<HTMLButtonElement | null>(null);

  const hasSession = session !== null && session.status !== "idle" && session.status !== "error";

  // The sidebar `rev` pill is a "go look" badge — once this panel is
  // mounted, the user is already looking. Register the channel with the
  // store for the panel's lifetime so the WS event handler and the
  // WS-reconnect rehydrate path skip re-adding the pill for this
  // channel. Unregister on unmount / channel switch.
  useEffect(() => {
    if (!registerReviewView) return;
    return registerReviewView(channelId);
  }, [channelId, registerReviewView]);

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

  // Persist mode + max_iterations to localStorage so the primary button
  // label and input survive remounts/reloads.
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(REVIEW_MODE_STORAGE_KEY, mode);
  }, [mode]);
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(REVIEW_MAX_ITER_STORAGE_KEY, String(maxIter));
  }, [maxIter]);

  // `hasChatPanel` is only consulted inside the `workflow.run_paused`
  // branch. Keep it in a ref so the WS subscription effect doesn't have
  // to tear down and re-subscribe whenever the parent layout re-renders
  // with a fresh `hasChatPanel` value.
  const hasChatPanelRef = useRef(hasChatPanel);
  useEffect(() => { hasChatPanelRef.current = hasChatPanel; }, [hasChatPanel]);

  // Workflow event subscription: chip mirrors run/node status for the
  // active review-loop run. Cleared on terminal status.
  useEffect(() => {
    if (!subscribeChatEvents) return;
    if (!loopRunId) {
      setLoopChip("");
      return;
    }
    const listener: ChatEventListener = (event: WSEvent) => {
      const d = event.data as { run_id?: string; status?: string; node_id?: string; iteration?: number } | undefined;
      if (!d || d.run_id !== loopRunId) return;
      switch (event.type) {
        case "workflow.run_started":
          setLoopChip("running iter 1");
          break;
        case "workflow.node_started":
          if (d.node_id) {
            const iter = typeof d.iteration === "number" ? d.iteration + 1 : 1;
            setLoopChip(`${d.node_id} — iter ${iter}`);
          }
          break;
        case "workflow.node_completed":
          // wait for next node_started or run_completed to overwrite
          break;
        case "workflow.run_paused":
          setLoopChip(hasChatPanelRef.current ? "paused at gate — see chat" : "paused at gate — approve below");
          break;
        case "workflow.run_completed":
          if (d.status === "completed") setLoopChip("done");
          else if (d.status === "failed") setLoopChip("failed");
          else if (d.status === "cancelled") setLoopChip("cancelled");
          else setLoopChip(d.status ?? "done");
          break;
        default:
      }
    };
    return subscribeChatEvents(listener);
  }, [subscribeChatEvents, loopRunId]);

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

  const runMode = useCallback(async (m: ReviewMode) => {
    setBusy(true); setError(null);
    setMode(m);
    // Optimistically flip to "reviewing" so the Run button stays disabled
    // even before the review.status WS event lands — otherwise there's a
    // brief window after setBusy(false) where status is still "ready" and
    // the button re-enables itself.
    setSession((prev) => prev ? { ...prev, status: "reviewing" } : prev);
    setLoopChip("starting...");
    try {
      const workflowName = m === "review-fix" ? REVIEW_FIX_LOOP_WORKFLOW : REVIEW_LOOP_WORKFLOW;
      const resp = await startWorkflowRun({
        workflow_name: workflowName,
        channel_id: channelId,
        inputs: { max_iterations: String(maxIter) },
      });
      setLoopRunId(resp.run_id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSession((prev) => prev ? { ...prev, status: "ready" } : prev);
      setLoopChip("");
    } finally {
      setBusy(false);
    }
  }, [channelId, maxIter]);

  const onPrimaryClick = useCallback(() => {
    void runMode(mode);
  }, [runMode, mode]);

  const openModeMenu = useCallback(() => {
    const el = caretRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setMenuPos({ x: r.left, y: r.bottom + 2 });
  }, []);

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
    const pending = (session?.comments ?? []).filter((c) => !c.pushed);
    if (pending.length === 0) return;
    // Toggle `busy` for the duration of the sendMessage round-trip so the
    // header buttons (which all gate on `busy`) actually disable. Without
    // this the disabled={busy} on "Push all to chat" was a no-op and
    // double-clicks queued duplicate prompts to the agent.
    setBusy(true); setError(null);
    try {
      ensureChatOpen();
      const prompt = buildBatchPromptForChat(pending, session?.head_sha, session?.pr?.number);
      await sendMessage(channelId, prompt);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
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
            <input
              data-testid="review-max-iter"
              type="number"
              min={1}
              max={10}
              value={maxIter}
              onChange={(e) => {
                const v = parseInt(e.target.value, 10);
                if (!Number.isFinite(v)) return;
                setMaxIter(Math.min(10, Math.max(1, v)));
              }}
              disabled={runDisabled}
              title="Maximum review iterations (1-10)"
              style={{
                width: 40,
                background: "transparent",
                color: colors.text,
                border: `1px solid ${colors.border}`,
                borderRadius: 4,
                padding: "3px 4px",
                fontSize: 11,
                fontFamily: fonts.sans,
                textAlign: "right",
              }}
            />
            <div style={{ display: "flex", alignItems: "stretch" }}>
              <button
                data-testid="review-run-btn"
                onClick={onPrimaryClick}
                disabled={runDisabled}
                style={{
                  ...(runDisabled ? { ...primaryBtnStyle, ...disabledStyle } : primaryBtnStyle),
                  borderTopRightRadius: 0,
                  borderBottomRightRadius: 0,
                  borderRight: "none",
                }}
                title={
                  session?.status === "reviewing"
                    ? "Review already running"
                    : `Start ${mode === "review-fix" ? "review + fix loop" : "review loop"}`
                }
              >
                {session?.status === "reviewing" ? "Running..." : modePrimaryLabel(mode)}
              </button>
              <button
                ref={caretRef}
                data-testid="review-run-mode-caret"
                onClick={openModeMenu}
                disabled={runDisabled}
                aria-label="Choose review run mode"
                style={{
                  ...(runDisabled ? { ...primaryBtnStyle, ...disabledStyle } : primaryBtnStyle),
                  borderTopLeftRadius: 0,
                  borderBottomLeftRadius: 0,
                  paddingLeft: 6,
                  paddingRight: 6,
                }}
                title="Choose review run mode"
              >
                ▾
              </button>
            </div>
            {loopChip && (
              <span
                data-testid="review-loop-chip"
                style={{
                  fontSize: 10,
                  fontFamily: fonts.sans,
                  color: colors.textDim,
                  padding: "2px 6px",
                  border: `1px solid ${colors.border}`,
                  borderRadius: 10,
                }}
                title={`Workflow run ${loopRunId ?? ""}`}
              >
                {loopChip}
              </span>
            )}
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
                  disabled={busy}
                  style={busy ? { ...btnStyle, ...disabledStyle } : btnStyle}
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
        {/* Inline gate approval card — only rendered when no chat panel is
            mounted in the current layout. When chat is mounted, ChatMessages
            renders the same card and the panel chip points the user there. */}
        {!hasChatPanel && gateApprovals?.chat && (
          <div data-testid="review-inline-approval" style={{ padding: 8, borderBottom: `1px solid ${colors.border}` }}>
            <ApprovalCard
              data={gateApprovals.chat}
              channelId={channelId}
              onResolved={() => onClearGateApproval?.("chat")}
            />
          </div>
        )}
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
      {menuPos && (
        <ContextMenu
          x={menuPos.x}
          y={menuPos.y}
          onClose={() => setMenuPos(null)}
          items={[
            {
              label: "Run review (one-shot)",
              onClick: () => void runMode("review-only"),
            },
            {
              label: "Run review + fix loop",
              onClick: () => void runMode("review-fix"),
            },
          ]}
        />
      )}
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

