import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchAgentConfig, sendMessage } from "../../api/channels";
import {
  approveReview,
  deleteReviewComment,
  deleteReviewSession,
  getReviewSession,
  listReviewPRs,
  loadReviewPR,
  pushAllReviewComments,
  pushReviewComment,
  type ReviewComment,
  type ReviewForkMode,
  type ReviewPR,
  type ReviewScope,
  type ReviewSession,
  type ReviewStatus,
  type ReviewVerdict,
  setReviewAgent,
  setReviewFork,
  setReviewScope,
  startReview,
  stopReview,
  syncReviewSession,
} from "../../api/review";
import { cancelWorkflowRun, FetchWorkflowRunError, fetchWorkflowRun, fetchWorkflowRuns, type WorkflowRun } from "../../api/workflows";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { GateApprovalRequestedData, WSEvent } from "../../types";
import { ApprovalCard } from "../chat/ApprovalCard";
import { EFFORT_PRESETS, shortModel } from "../chat/agentPresets";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";
import { ReviewApproveDialog } from "./ReviewApproveDialog";
import { ReviewDiffView } from "./ReviewDiffView";
import { ReviewRunDrawer } from "./ReviewRunDrawer";
import { ReviewStatsView } from "./ReviewStatsView";
import { approveBody, approveDisabledReason, canApprove, deriveOutcome, isApprovedAtHead, outcomeColor, outcomeLabel, outcomeTitle } from "./reviewOutcome";

type ReviewMode = "review-only" | "review-fix";

const REVIEW_MODE_STORAGE_KEY = "loop.review.lastMode";

const REVIEW_LOOP_WORKFLOW = "review-loop";
const REVIEW_FIX_LOOP_WORKFLOW = "review-fix-loop";

function isReviewWorkflow(name: string | undefined): name is string {
  return name === REVIEW_LOOP_WORKFLOW || name === REVIEW_FIX_LOOP_WORKFLOW;
}

/** A review run the panel didn't start but should show. */
export interface AdoptableReviewRun {
  runId: string;
  mode: ReviewMode;
}

function adoptable(runId: string, workflowName: string): AdoptableReviewRun {
  return { runId, mode: workflowName === REVIEW_FIX_LOOP_WORKFLOW ? "review-fix" : "review-only" };
}

/** The channel's review workflow run that is still running or paused, if any. */
export function liveReviewRun(runs: Pick<WorkflowRun, "id" | "workflow_name" | "status">[]): AdoptableReviewRun | null {
  const run = runs.find((r) => isReviewWorkflow(r.workflow_name) && (r.status === "running" || r.status === "paused"));
  return run ? adoptable(run.id, run.workflow_name) : null;
}

/** The review run a workflow.run_started event starts in channelId, if it is one. */
export function startedReviewRun(event: WSEvent, channelId: string): AdoptableReviewRun | null {
  if (event.type !== "workflow.run_started") return null;
  const d = event.data as { run_id?: string; workflow_name?: string; channel_id?: string } | undefined;
  if (!d?.run_id || d.channel_id !== channelId || !isReviewWorkflow(d.workflow_name)) return null;
  return adoptable(d.run_id, d.workflow_name);
}

// Per-channel sessionStorage key for the currently active review-loop run.
// We store this in sessionStorage (not localStorage) so unrelated tabs don't
// fight over the same run, but the chip survives a panel remount within the
// same tab (e.g. layout reshuffle, dev-tools open/close, hot reload).
function reviewRunIdKey(channelId: string): string {
  return `loop.review.activeRunId.${channelId}`;
}

// Per-channel key for the run whose canvas the bottom drawer shows. Distinct
// from the active-run key above: the drawer keeps showing a run's final canvas
// after it completes (when the active-run key is cleared), so the user can
// still inspect what happened. Overwritten when a new run starts; cleared when
// the session is closed.
function reviewDrawerRunIdKey(channelId: string): string {
  return `loop.review.drawerRunId.${channelId}`;
}

const REVIEW_DRAWER_COLLAPSED_KEY = "loop.review.drawerCollapsed";

// CustomEvent name used to broadcast review-run-id sessionStorage writes
// across sibling ReviewPanel instances mounted in the same window. The
// browser's native `storage` event only fires for OTHER documents, so two
// ReviewPanels mounted side-by-side (split layout, dual-pane review) for
// the same channel wouldn't observe each other's writes without this.
const REVIEW_RUN_ID_EVENT = "loop.review.activeRunId.changed";
interface ReviewRunIdChangedDetail {
  channelId: string;
  runId: string | null;
}
function broadcastReviewRunIdChange(channelId: string, runId: string | null): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(
    new CustomEvent<ReviewRunIdChangedDetail>(REVIEW_RUN_ID_EVENT, {
      detail: { channelId, runId },
    }),
  );
}

function readStoredMode(): ReviewMode {
  if (typeof window === "undefined") return "review-only";
  const v = window.localStorage.getItem(REVIEW_MODE_STORAGE_KEY);
  return v === "review-fix" ? "review-fix" : "review-only";
}

// DEFAULT_REVIEW_MAX_ITER is review.max_iterations' default, for a channel
// that doesn't carry it.
const DEFAULT_REVIEW_MAX_ITER = 3;

function modePrimaryLabel(mode: ReviewMode): string {
  return mode === "review-fix" ? "Run review + fix" : "Run review";
}

// The agent runs inside the channel's worktree, so it has direct
// filesystem access — we only hand it enough metadata to locate the
// comment in the PR's diff and the original review note verbatim.
function sideLabel(side?: string): string {
  return side === "LEFT" ? "deleted/old" : "added/new";
}

// buildAddressPrompt is what "Address" sends: the metadata the agent needs to
// locate the line, the finding verbatim, and the transcripts of the runs that
// produced it. It was two buttons until it wasn't — "Push to chat" sent the
// metadata and "Address" sent the transcripts, which left the user choosing
// between two phrasings of the same request. One button now carries both.
export function buildAddressPrompt(c: ReviewComment, session?: ReviewSession | null): string {
  const lines: string[] = [];
  lines.push(`Please address this review comment from the PR:`);
  lines.push("");
  lines.push(`- File: \`${c.path}\``);
  lines.push(`- Line: ${c.line} (${c.side || "RIGHT"} — ${sideLabel(c.side)})`);
  if (session?.pr?.number) lines.push(`- PR: #${session.pr.number}`);
  if (session?.head_sha) lines.push(`- Commit: ${session.head_sha}`);
  if (c.author) lines.push(`- Author: @${c.author}`);
  lines.push("");
  lines.push(`Comment:`);
  lines.push("");
  for (const ln of c.body.split("\n")) lines.push(`> ${ln}`);
  const refs = transcriptRefs(c);
  if (refs.length > 0) {
    lines.push("");
    lines.push(`Transcripts of the review passes behind this, oldest first:`);
    for (const ref of refs) lines.push(`- ${ref}`);
  }
  return lines.join("\n");
}

// transcriptRefs lists the transcripts of the agent passes that reported or
// changed c, oldest first. Each finding carries its own, even in a batch,
// so the agent knows which transcript explains which finding. The reasoning behind a
// finding lives in those files and nowhere else — the panel only keeps the
// verdict. Full paths, not bare session ids: Claude keys transcripts by the
// agent's CWD, which for a review run is the PR worktree, so the directory is
// not one the chat agent can derive from its own channel. The paths resolve
// as-is inside the agent container, which runs with HOME set to the host home
// and ~/.claude bind-mounted at its host path.
function transcriptRefs(c: ReviewComment): string[] {
  return (c.transcripts ?? []).filter((t) => t);
}

// buildDiscussDraft renders a finding as a quoted block to drop into the
// chat composer, so the user can type their question under it and send it
// themselves. Unlike buildAddressPrompt this is deliberately not an
// instruction to the agent: "Discuss" is for asking why a finding was made,
// which is a different intent from "go fix this".
//
// The path:line header rides inside the quote because the body alone rarely
// says where it applies, and the agent can't act on "explain this" without
// somewhere to look.

// The canned ask. It sits next to the builder rather than inside it so a test
// can assert the exact wording that goes out, and so the difference between
// Discuss and Why? stays one string rather than one code path each.
/** Reasoning-effort levels worth running a review at; lower ones miss findings. */
export const RECOMMENDED_REVIEW_EFFORTS = ["high", "xhigh", "max"];

export interface SelectOption {
  value: string;
  label: string;
}

/**
 * Model choices for a review run: the config default first (named, so the
 * fallback is concrete), then the daemon's models. A current id outside them
 * — set from the CLI, or a model since dropped — is kept as an option so the
 * select can still show it.
 */
export function reviewModelOptions(current: string, defaultModel: string, models: string[]): SelectOption[] {
  const ids = current && !models.includes(current) ? [...models, current] : models;
  return [{ value: "", label: defaultModel ? `Default (${shortModel(defaultModel)})` : "Default model" }, ...ids.map((m) => ({ value: m, label: shortModel(m) }))];
}

/** Effort choices for a review run, with the recommended levels marked. */
export function reviewEffortOptions(defaultEffort: string): SelectOption[] {
  const mark = (e: string) => (RECOMMENDED_REVIEW_EFFORTS.includes(e) ? `${e} (recommended)` : e);
  return [{ value: "", label: defaultEffort ? `Default (${defaultEffort})` : "Default effort" }, ...EFFORT_PRESETS.map((e) => ({ value: e, label: mark(e) }))];
}

export const REVIEW_SCOPE_OPTIONS: { value: ReviewScope; label: string }[] = [
  { value: "", label: "Auto scope" },
  { value: "incremental", label: "Changes since last review" },
  { value: "full", label: "Whole PR" },
];

// reviewScopeChip says what the latest run reviewed, or "" before any run.
export function reviewScopeChip(session?: ReviewSession | null): string {
  if (session?.last_run_scope === "incremental" && session.last_run_from_sha) return `since ${session.last_run_from_sha.slice(0, 7)}`;
  if (session?.last_run_scope === "full") return "whole PR";
  return "";
}

export const WHY_QUESTION = "Please explain why we need this.";

// withoutComment drops comment id from the session, for a
// `review.comment_removed` event.
export function withoutComment(session: ReviewSession | null, id: string): ReviewSession | null {
  if (!session) return session;
  return { ...session, comments: session.comments.filter((c) => c.id !== id) };
}

// withUpdatedComment swaps in the fields of a `review.comment_updated`
// event, keeping the rest (pushed state, GitHub link) of the held comment.
// The event leaves out empty fields, so the verdict, its reason and the
// impact are taken as given: one missing was cleared (a body edit clears
// them), not left as it was.
export function withUpdatedComment(session: ReviewSession | null, c: Partial<ReviewComment> & { id: string }): ReviewSession | null {
  if (!session) return session;
  return {
    ...session,
    comments: session.comments.map((x) => (x.id === c.id ? { ...x, ...c, verdict: c.verdict, verdict_reason: c.verdict_reason, impact: c.impact } : x)),
  };
}

// withVerdictState takes the parts of a fresh GET that only change behind
// the comment events (thread assessments, the reviewed and approved heads,
// the latest review's runs) into the held session.
export function withVerdictState(session: ReviewSession | null, fresh: ReviewSession): ReviewSession | null {
  if (!session) return session;
  return {
    ...session,
    threads: fresh.threads,
    reviewed_sha: fresh.reviewed_sha,
    approved_sha: fresh.approved_sha,
    review_id: fresh.review_id,
    review_runs: fresh.review_runs,
  };
}

// withThreads hands each GitHub comment that starts a thread the
// verification step's assessment of it, for its badge.
export function withThreads(comments: ReviewComment[], threads?: ReviewSession["threads"]): ReviewComment[] {
  if (!threads) return comments;
  return comments.map((c) => {
    const a = c.source === "github" && !c.reply_to && c.github_id ? threads[String(c.github_id)] : undefined;
    return a ? { ...c, thread: a } : c;
  });
}

export function buildDiscussDraft(c: ReviewComment, ask?: string): string {
  const lines = [`> ${c.path}:${c.line}`];
  for (const ln of c.body.split("\n")) lines.push(ln ? `> ${ln}` : ">");
  const refs = transcriptRefs(c);
  if (refs.length > 0) {
    lines.push(">");
    lines.push("> transcripts of the review passes behind this, oldest first:");
    for (const ref of refs) lines.push(`> ${ref}`);
  }
  // Trailing blank line: markdown needs one to close the blockquote, and it
  // puts the caret on an empty line instead of at the end of the quote.
  const draft = `${lines.join("\n")}\n\n`;
  // Why? is this same draft with its question filled into the blank line the
  // quote ends on, which is where the user would have typed it.
  return ask ? `${draft}${ask}` : draft;
}

// The batch form of buildAddressPrompt: same request, same metadata, same
// trailing transcripts — the findings are numbered blocks instead of one quote.
// A transcript often covers several of the findings (one review run reports
// many), so the transcripts are listed once at the end rather than repeated
// under every block.
export function buildAddressAllPrompt(cs: ReviewComment[], session?: ReviewSession | null): string {
  const prNumber = session?.pr?.number;
  const headSHA = session?.head_sha;
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
    const refs = transcriptRefs(c);
    if (refs.length > 0) {
      b.push("");
      b.push(`Transcripts of the review passes behind this, oldest first:`);
      for (const ref of refs) b.push(`- ${ref}`);
    }
    return b.join("\n");
  });
  return [...header, ...blocks].join("\n");
}

// buildCheckAllPrompt asks the chat agent to verify this session's own
// findings (the "new" ones, not the GitHub comments), after the dedup pass
// has merged the ones that repeat each other. It doesn't quote them: the
// agent reads them with get_review_comments, which acts on the same
// channel's session, so the request stays short however many there are and
// the agent sees each body whole. The count is left out because the dedup
// pass changes it. The checkout is named because the review runs in the PR
// worktree, which need not be the chat's working directory.
export function buildCheckAllPrompt(session?: ReviewSession | null): string {
  const lines = ["Please check the review comments this review session reported on the PR."];
  const prNumber = session?.pr?.number;
  const headSHA = session?.head_sha;
  const checkout = session?.worktree_path;
  const prURL = session?.pr?.url;
  if (prNumber || headSHA || checkout) lines.push("");
  if (prNumber) lines.push(`- PR: #${prNumber}`);
  if (headSHA) lines.push(`- Commit: ${headSHA}`);
  if (checkout) lines.push(`- Checkout: ${checkout}`);
  lines.push(
    "",
    "1. Run the dedup_review_findings tool first, so comments that report the same issue are merged before you check them. If it fails, say why and carry on.",
    "2. Read the comments left with the get_review_comments tool. Skip the ones whose source is github.",
    `3. For each one, read the code it points at${checkout ? " in the checkout above" : ""} and decide whether it is a real issue, a false positive, or already fixed.`,
    "4. Record your verdict on each one with the update_review_comment tool: its comment_id, verdict (real, false_positive or already_fixed) and a one-sentence reason citing the line you read. Don't pass a body. Your verdict replaces the dedup pass's, so the Review panel shows yours.",
    `5. Reply with one entry per comment: its id, path:line, category (when it has one), verdict and why. Link a pushed comment to ${prURL ? `${prURL}#discussion_r<its GitHub id>` : "its GitHub thread"}. Only show a severity the comment itself states; don't rate them yourself.`,
    "6. End with what the dedup pass removed and how many comments got each verdict.",
    "",
    "Don't change any code. Apart from the dedup pass and recording verdicts, don't edit, delete or push any comment.",
  );
  return lines.join("\n");
}

// isActionable reports whether Address all and Push all take c: it isn't
// pushed yet, and the dedup pass didn't mark it false_positive or
// already_fixed. A comment with no verdict yet counts.
export function isActionable(c: ReviewComment): boolean {
  return !c.pushed && c.verdict !== "false_positive" && c.verdict !== "already_fixed";
}

// isBlocking reports whether Address blocking and Push blocking take c: an
// actionable finding judged blocking.
export function isBlocking(c: ReviewComment): boolean {
  return isActionable(c) && c.impact === "blocking";
}

// skippedNote names the unpushed comments Address all and Push all leave
// out, in the verdict badges' words: " · skips 1 false positive, 2 already
// fixed". It's empty when they leave none out.
export function skippedNote(comments: ReviewComment[]): string {
  const count = (v: ReviewVerdict) => comments.filter((c) => !c.pushed && c.verdict === v).length;
  const parts: string[] = [];
  const fp = count("false_positive");
  const af = count("already_fixed");
  if (fp > 0) parts.push(`${fp} false positive`);
  if (af > 0) parts.push(`${af} already fixed`);
  return parts.length > 0 ? ` · skips ${parts.join(", ")}` : "";
}

// allActionItems lists the header's "All" menu: the actions over every
// comment of a kind, each with its count, and only those with something
// to act on. skipped is skippedNote's text, which Address all and Push all
// carry so their count reads against Check all's. The blocking variants
// show when some, but not all, of those are blocking. A click while another
// action is busy does nothing, since the menu can stay open while one
// starts.
export function allActionItems(a: {
  newCount: number;
  pendingCount: number;
  blockingCount?: number;
  skipped?: string;
  busy: boolean;
  onCheckAll: () => void;
  onAddressAll: () => void;
  onPushAll: () => void;
  onAddressBlocking?: () => void;
  onPushBlocking?: () => void;
}): MenuItem[] {
  const guard = (fn: () => void) => () => {
    if (!a.busy) fn();
  };
  const items: MenuItem[] = [];
  if (a.newCount > 0) items.push({ label: `Check all (${a.newCount})`, onClick: guard(a.onCheckAll) });
  if (a.pendingCount > 0) {
    const skipped = a.skipped ?? "";
    const blocking = a.blockingCount ?? 0;
    const some = blocking > 0 && blocking < a.pendingCount;
    items.push({ label: `Address all (${a.pendingCount})${skipped}`, onClick: guard(a.onAddressAll) });
    if (some && a.onAddressBlocking) items.push({ label: `Address blocking only (${blocking})`, onClick: guard(a.onAddressBlocking) });
    items.push({ label: `Push all to GitHub (${a.pendingCount})${skipped}`, onClick: guard(a.onPushAll) });
    if (some && a.onPushBlocking) items.push({ label: `Push blocking to GitHub (${blocking})`, onClick: guard(a.onPushBlocking) });
  }
  return items;
}

// runSettingsSummary is the gear's tooltip while the settings row is hidden:
// what the next run goes with.
export function runSettingsSummary(a: { maxIter: number; model: string; effort: string; forkMode: ReviewForkMode; scope: ReviewScope }): string {
  const fork = a.forkMode === "current" ? "fork chat session" : a.forkMode === "custom" ? "fork session id" : "fresh session";
  const scope = REVIEW_SCOPE_OPTIONS.find((o) => o.value === a.scope)?.label ?? a.scope;
  return [`${a.maxIter} iteration${a.maxIter === 1 ? "" : "s"}`, a.model ? shortModel(a.model) : "default model", a.effort || "default effort", fork, scope].join(", ");
}

// moreMenuItems lists the header's ⋯ menu, the actions that don't need to
// sit in the header: Sync, the All actions (see allActionItems), Approve
// anyway when the outcome doesn't allow a plain approval, Stats and Close.
// An item whose action can't run now is left out, or does nothing when
// clicked, as allActionItems' are.
export function moreMenuItems(a: {
  syncDisabled: boolean;
  all: MenuItem[];
  approveAnyway: boolean;
  showStats: boolean;
  closeDisabled: boolean;
  onSync: () => void;
  onApproveAnyway: () => void;
  onToggleStats: () => void;
  onClose: () => void;
}): MenuItem[] {
  const items: MenuItem[] = [];
  if (!a.syncDisabled) items.push({ label: "Sync with GitHub", onClick: a.onSync });
  items.push(...a.all);
  if (a.approveAnyway) items.push({ label: "Approve anyway…", onClick: a.onApproveAnyway });
  // A separator draws its line above its own item, so Stats carries it.
  items.push({ label: a.showStats ? "Back to the review" : "Stats", onClick: a.onToggleStats, separator: items.length > 0 });
  if (!a.closeDisabled) items.push({ label: "Close review", onClick: a.onClose, danger: true });
  return items;
}

interface ReviewPanelProps {
  channelId: string;
  /** The project's review.max_iterations: the passes input's starting value. */
  defaultMaxIter?: number;
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
    case "idle":
      return "Idle";
    case "loading":
      return "Loading PR...";
    case "ready":
      return "Ready";
    case "reviewing":
      return "Reviewing...";
    case "verifying":
      return "Verifying...";
    case "error":
      return "Error";
  }
}

export function ReviewPanel({ channelId, defaultMaxIter = DEFAULT_REVIEW_MAX_ITER, subscribeChatEvents, registerReviewView, hasChatPanel, gateApprovals, onClearGateApproval }: ReviewPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [session, setSession] = useState<ReviewSession | null>(null);
  // A run's verification step holds the session like the run does.
  const verifying = session?.status === "verifying";
  const reviewRunning = session?.status === "reviewing" || verifying;
  const [prList, setPrList] = useState<ReviewPR[] | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadingPR, setLoadingPR] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<ReviewMode>(() => readStoredMode());
  // Starts from the project's review.max_iterations, and again when that
  // changes; editing it applies to the runs this panel starts.
  const [maxIter, setMaxIter] = useState<number>(defaultMaxIter);
  useEffect(() => setMaxIter(defaultMaxIter), [defaultMaxIter]);
  // Hydrate loopRunId from sessionStorage so a panel remount (layout
  // reshuffle, dev-tools, hot reload) re-binds to the still-running daemon
  // workflow instead of losing track of it. The hydration effect below
  // verifies the run is still live and either resyncs the chip or clears
  // the key on terminal status.
  const [loopRunId, setLoopRunId] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    return window.sessionStorage.getItem(reviewRunIdKey(channelId));
  });
  const [loopChip, setLoopChip] = useState<string>("");
  // The run whose canvas the bottom drawer shows. Survives run completion (so
  // the final canvas stays visible) — see reviewDrawerRunIdKey.
  const [drawerRunId, setDrawerRunId] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    return window.sessionStorage.getItem(reviewDrawerRunIdKey(channelId));
  });
  const [drawerCollapsed, setDrawerCollapsed] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return window.localStorage.getItem(REVIEW_DRAWER_COLLAPSED_KEY) === "1";
  });
  // Tracks whether a workflow run is in flight independently of
  // `session.status` and `busy`. The review-fix loop's body children flip the
  // daemon's review session back to "ready" between iterations (after each
  // `review` child completes), and `busy` clears as soon as the initial POST
  // returns — without this guard, the primary button would re-enable
  // mid-loop and let the user start a second concurrent run on the same
  // worktree. Cleared on workflow.run_completed.
  const [loopActive, setLoopActive] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return window.sessionStorage.getItem(reviewRunIdKey(channelId)) !== null;
  });
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const caretRef = useRef<HTMLButtonElement | null>(null);
  // The fork control is edited locally and committed to the daemon on
  // change/blur. Two drafts because "custom" is only a legal server-side
  // choice once an id exists — the dropdown can sit on it while the
  // adjacent input is still empty.
  const [forkModeDraft, setForkModeDraft] = useState<ReviewForkMode>("");
  const [forkDraft, setForkDraft] = useState("");
  // The config's model/effort for this channel, so the "Default" choices
  // name what they fall back to.
  const [agentDefaults, setAgentDefaults] = useState<{ model: string; effort: string; models: string[] }>({ model: "", effort: "", models: [] });
  const [showStats, setShowStats] = useState(false);
  const [moreMenuPos, setMoreMenuPos] = useState<{ x: number; y: number } | null>(null);
  const moreBtnRef = useRef<HTMLButtonElement | null>(null);
  // The run settings (iterations, model, effort, fork, scope) sit in a row
  // of their own under the header, shown on demand.
  const [showSettings, setShowSettings] = useState(false);
  // The approve dialog's text; null while it's closed. force approves past
  // an outcome that doesn't allow it.
  const [approveDraft, setApproveDraft] = useState<{ body: string; force: boolean } | null>(null);

  const hasSession = session !== null && session.status !== "idle" && session.status !== "error";
  const outcome = useMemo(() => (session ? deriveOutcome(session) : null), [session]);
  const threadedComments = useMemo(() => withThreads(session?.comments ?? [], session?.threads), [session]);

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
    setForkModeDraft("");
    setForkDraft("");
    setAgentDefaults({ model: "", effort: "", models: [] });
    setShowStats(false);
    setApproveDraft(null);
    fetchAgentConfig(channelId)
      .then((cfg) => {
        if (!cancelled) setAgentDefaults({ model: cfg.default_model, effort: cfg.default_effort, models: cfg.models ?? [] });
      })
      .catch(() => {});
    (async () => {
      try {
        const resp = await getReviewSession(channelId);
        if (cancelled) return;
        if (resp.present && resp.session) {
          setSession(resp.session);
          // Seed the fork control from the daemon's copy. Only here —
          // later setSession calls (status flips, diff swaps) must not
          // clobber a half-typed custom session id.
          setForkModeDraft(resp.session.fork_mode ?? "");
          setForkDraft(resp.session.fork_session_id ?? "");
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
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
    return () => {
      cancelled = true;
    };
  }, [channelId, hasSession]);

  // Persist mode to localStorage so the primary button label survives
  // remounts/reloads.
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(REVIEW_MODE_STORAGE_KEY, mode);
  }, [mode]);
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(REVIEW_DRAWER_COLLAPSED_KEY, drawerCollapsed ? "1" : "0");
  }, [drawerCollapsed]);

  const onToggleDrawer = useCallback(() => setDrawerCollapsed((c) => !c), []);

  // `hasChatPanel` is only consulted inside the `workflow.run_paused`
  // branch. Keep it in a ref so the WS subscription effect doesn't have
  // to tear down and re-subscribe whenever the parent layout re-renders
  // with a fresh `hasChatPanel` value.
  const hasChatPanelRef = useRef(hasChatPanel);
  useEffect(() => {
    hasChatPanelRef.current = hasChatPanel;
  }, [hasChatPanel]);

  // Cross-instance loopActive/loopRunId sync. Two ReviewPanels mounted in
  // the same window for the same channel (split layout, dual-pane) only read
  // sessionStorage at lazy-init. Without this listener, instance B would keep
  // showing the Run button enabled while instance A has a workflow in flight,
  // letting the user start a second concurrent run on the same worktree.
  // `storage` events fire only for OTHER documents/tabs, so we layer a
  // window-level CustomEvent for same-tab siblings on top.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onCustom = (ev: Event) => {
      const detail = (ev as CustomEvent<ReviewRunIdChangedDetail>).detail;
      if (!detail || detail.channelId !== channelId) return;
      if (detail.runId === null) {
        setLoopRunId(null);
        setLoopActive(false);
      } else {
        setLoopRunId(detail.runId);
        setLoopActive(true);
        setDrawerRunId(detail.runId);
      }
    };
    const onStorage = (ev: StorageEvent) => {
      if (ev.storageArea !== window.sessionStorage) return;
      if (ev.key !== reviewRunIdKey(channelId)) return;
      if (ev.newValue === null) {
        setLoopRunId(null);
        setLoopActive(false);
      } else {
        setLoopRunId(ev.newValue);
        setLoopActive(true);
        setDrawerRunId(ev.newValue);
      }
    };
    window.addEventListener(REVIEW_RUN_ID_EVENT, onCustom);
    window.addEventListener("storage", onStorage);
    return () => {
      window.removeEventListener(REVIEW_RUN_ID_EVENT, onCustom);
      window.removeEventListener("storage", onStorage);
    };
  }, [channelId]);

  // Adopt a review run started elsewhere (the run_review MCP tool, the CLI,
  // another window) as if the Run button had started it: on mount when a
  // review workflow is already running or paused for the channel, and when
  // one starts while the panel is open. A run started elsewhere may have
  // loaded a PR too, so the live case re-reads the session.
  const loopRunIdRef = useRef(loopRunId);
  useEffect(() => {
    loopRunIdRef.current = loopRunId;
  }, [loopRunId]);
  const adoptRun = useCallback(
    ({ runId, mode }: AdoptableReviewRun, rereadSession: boolean) => {
      if (loopRunIdRef.current === runId) return;
      loopRunIdRef.current = runId;
      if (typeof window !== "undefined") {
        window.sessionStorage.setItem(reviewRunIdKey(channelId), runId);
        window.sessionStorage.setItem(reviewDrawerRunIdKey(channelId), runId);
      }
      broadcastReviewRunIdChange(channelId, runId);
      setDrawerRunId(runId);
      setLoopRunId(runId);
      setLoopActive(true);
      setLoopChip("running");
      setMode(mode);
      if (!rereadSession) return;
      getReviewSession(channelId)
        .then((resp) => {
          if (!resp.present || !resp.session) return;
          setSession(resp.session);
          setForkModeDraft(resp.session.fork_mode ?? "");
          setForkDraft(resp.session.fork_session_id ?? "");
        })
        .catch(() => {});
    },
    [channelId],
  );
  useEffect(() => {
    if (loopRunIdRef.current) return;
    let cancelled = false;
    fetchWorkflowRuns(channelId, 20)
      .then((runs) => {
        if (cancelled || loopRunIdRef.current) return;
        const run = liveReviewRun(runs);
        if (run) adoptRun(run, false);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [channelId, adoptRun]);
  useEffect(() => {
    if (!subscribeChatEvents) return;
    return subscribeChatEvents((event: WSEvent) => {
      const run = startedReviewRun(event, channelId);
      if (run) adoptRun(run, true);
    });
  }, [channelId, subscribeChatEvents, adoptRun]);

  // Hydration resync: when the panel mounts (or the channel changes) with
  // a sessionStorage-recovered run id, ask the daemon for the current
  // status. Three outcomes:
  //   - run is terminal already (completed/failed/cancelled) → clear the
  //     key and show the matching final chip so the user sees what
  //     happened during their absence.
  //   - run is still live → leave the key alone; the chip will be driven
  //     by the workflow event subscription below as new events arrive.
  //   - the run row is gone (deleted/404) → clear the key silently.
  // Without this resync, a run that finished while the panel was unmounted
  // would leave loopActive=true forever (sticky disabled button) because
  // workflow.run_completed already fired and won't fire again.
  useEffect(() => {
    if (!loopRunId) return;
    let cancelled = false;
    const ctl = new AbortController();
    // Bounded retry on transient failures (5xx, TypeError "Failed to fetch",
    // JSON parse). Without this, a daemon restart that took longer than the
    // initial hydration would leave loopActive=true forever — because the WS
    // run_completed event already fired and won't fire again on remount.
    // Three retries with exponential backoff (1s, 2s, 4s) cover a typical
    // restart window without spamming the daemon during a longer outage.
    const transientBackoffMs = [1000, 2000, 4000];
    let attempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      try {
        const detail = await fetchWorkflowRun(loopRunId, { signal: ctl.signal });
        if (cancelled) return;
        const status = detail.run?.status;
        if (status === "completed" || status === "failed" || status === "cancelled") {
          if (typeof window !== "undefined") {
            window.sessionStorage.removeItem(reviewRunIdKey(channelId));
          }
          broadcastReviewRunIdChange(channelId, null);
          setLoopChip(status === "completed" ? "done" : status);
          setLoopActive(false);
          setLoopRunId(null);
        } else if (status === "paused") {
          setLoopChip(hasChatPanelRef.current ? "paused at gate — see chat" : "paused at gate — approve below");
        } else {
          // Still running — chip will be repainted by the WS subscription.
          setLoopChip((prev) => prev || "running");
        }
      } catch (e) {
        if (cancelled) return;
        // Aborted by cleanup — leave state alone; a fresh effect will fire
        // for the new loopRunId.
        if (e instanceof DOMException && e.name === "AbortError") return;
        // Distinguish "run row is gone" (404) from "daemon hiccup" (5xx /
        // network / JSON parse). Only the former should clear the stale
        // handle; a momentary daemon restart shouldn't wipe a live loop's
        // UI state.
        if (e instanceof FetchWorkflowRunError && e.status === 404) {
          if (typeof window !== "undefined") {
            window.sessionStorage.removeItem(reviewRunIdKey(channelId));
          }
          broadcastReviewRunIdChange(channelId, null);
          setLoopChip("");
          setLoopActive(false);
          setLoopRunId(null);
          return;
        }
        // Transient: retry with backoff. After exhausting retries, clear
        // loopActive so the button isn't permanently disabled — the WS
        // subscription will repaint the chip if events resume.
        if (attempt < transientBackoffMs.length) {
          retryTimer = setTimeout(tick, transientBackoffMs[attempt]);
          attempt++;
          return;
        }
        setLoopActive(false);
        setLoopChip((prev) => prev || "unknown");
      }
    };
    void tick();
    return () => {
      cancelled = true;
      ctl.abort();
      if (retryTimer !== null) clearTimeout(retryTimer);
    };
    // Only fire on (channelId, loopRunId) edge transitions. Adding the
    // chat-panel ref or setters would re-trigger the API call needlessly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, loopRunId]);

  // Workflow event subscription: chip mirrors run/node status for the
  // active review-loop run. Cleared on terminal status.
  //
  // Importantly, do NOT call setLoopChip("") inside this effect when
  // loopRunId is null. The hydration effect above runs first on mount and
  // sets the chip (e.g. to "done" or "running") based on the recovered run's
  // status; this effect re-runs on the same mount with loopRunId still null,
  // wiping the chip the hydration effect just set. The hydration effect
  // owns the initial chip state for unmount→remount cases, and the per-run
  // listener only paints when there's an active loopRunId to listen to.
  useEffect(() => {
    if (!subscribeChatEvents) return;
    if (!loopRunId) return;
    const listener: ChatEventListener = (event: WSEvent) => {
      const d = event.data as { run_id?: string; status?: string; node_id?: string; iteration?: number } | undefined;
      if (!d || d.run_id !== loopRunId) return;
      switch (event.type) {
        case "workflow.run_started":
          setLoopChip("running iter 1");
          break;
        case "workflow.node_started":
          if (d.node_id === "dedup") {
            // review-loop's final pass runs after the loop, not in an iteration.
            setLoopChip("deduping comments");
          } else if (d.node_id === "check") {
            // So does the seeded loops' final verification.
            setLoopChip("verifying comments");
          } else if (d.node_id) {
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
          setLoopActive(false);
          // Drop the sessionStorage handle once the run hits a terminal
          // state, so a subsequent remount doesn't re-resync to a stale id.
          if (typeof window !== "undefined") {
            window.sessionStorage.removeItem(reviewRunIdKey(channelId));
          }
          broadcastReviewRunIdChange(channelId, null);
          // Also drop the in-memory loopRunId so the hydration effect
          // doesn't re-fire fetchWorkflowRun against this terminal id when
          // the user kicks off a new mode dispatch (which only toggles
          // loopActive). Without this, a fresh run-mode click re-queries
          // the stale id and the chip flickers through the terminal state
          // before the new run id mounts.
          setLoopRunId(null);
          break;
        default:
      }
    };
    return subscribeChatEvents(listener);
  }, [subscribeChatEvents, loopRunId, channelId]);

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
      } else if (event.type === "review.comment_updated") {
        // The dedup pass appended what a kept comment's dropped duplicates added.
        const c = event.data as ReviewComment;
        setSession((prev) => withUpdatedComment(prev, c));
      } else if (event.type === "review.comment_removed") {
        // Deleted here or in another window, or dropped as a duplicate by
        // the dedup pass.
        const { id } = event.data as { id: string };
        setSession((prev) => withoutComment(prev, id));
      } else if (event.type === "review.status") {
        const d = event.data as { status: ReviewStatus; error?: string };
        setSession((prev) => (prev ? { ...prev, status: d.status, error: d.error ?? "" } : prev));
        // Thread assessments, the reviewed head and the review's runs change
        // behind no event of their own; a run starting, or a run or
        // verification settling, is when they move.
        if (d.status === "ready" || d.status === "reviewing") {
          getReviewSession(channelId, { diff: false })
            .then((resp) => {
              const fresh = resp.session;
              if (fresh) setSession((prev) => withVerdictState(prev, fresh));
            })
            .catch(() => {});
        }
      } else if (event.type === "review.approved") {
        const d = event.data as { commit: string };
        setSession((prev) => (prev ? { ...prev, approved_sha: d.commit } : prev));
      } else if (event.type === "review.diff") {
        // Backend re-rendered the diff with widened context after an
        // agent comment landed outside the current hunks. Swap the
        // raw_diff in place — the diff view re-parses on the new value
        // and existing comments re-bind to the wider hunk set.
        const d = event.data as { raw_diff: string };
        setSession((prev) => (prev ? { ...prev, raw_diff: d.raw_diff } : prev));
      }
    };
    return subscribeChatEvents(listener);
  }, [channelId, subscribeChatEvents]);

  const onSelectPR = useCallback(
    async (pr: ReviewPR) => {
      setBusy(true);
      setError(null);
      setLoadingPR(pr.number);
      try {
        const resp = await loadReviewPR(channelId, pr.number);
        if (resp.present && resp.session) {
          setSession(resp.session);
          // A load starts a new session with the daemon's default fork
          // choice; seed the control from it like the initial fetch does.
          setForkModeDraft(resp.session.fork_mode ?? "");
          setForkDraft(resp.session.fork_session_id ?? "");
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(false);
        setLoadingPR(null);
      }
    },
    [channelId],
  );

  const runMode = useCallback(
    async (m: ReviewMode) => {
      setBusy(true);
      setError(null);
      setLoopChip("starting...");
      setLoopActive(true);
      try {
        // Re-verify the daemon-side session exists before dispatching the
        // workflow. The session lives in the daemon's in-memory reviewStore
        // and gets wiped on restart, while the FE may still hold a stale
        // session object in React state. Without this check the workflow
        // starts and immediately fails on the CLI's POST /review/run with
        // a cryptic "node loop failed: script exited with status 1".
        const server = await getReviewSession(channelId);
        if (!server.present || !server.session) {
          setSession(null);
          throw new Error("No review session loaded — pick a PR first, then run the loop.");
        }
        setSession(server.session);
        // Optimistically flip to "reviewing" so the Run button stays disabled
        // even before the review.status WS event lands — otherwise there's a
        // brief window after setBusy(false) where status is still "ready" and
        // the button re-enables itself.
        setSession((prev) => (prev ? { ...prev, status: "reviewing" } : prev));
        const prNumber = server.session.pr?.number;
        if (!prNumber) throw new Error("No review session loaded — pick a PR first, then run the loop.");
        // review/start, not a bare workflow run: it starts a new review, so
        // the "new" comments are this loop's, and pins a review-only loop to
        // the head it starts at. The model and effort are the session's,
        // which the settings row keeps.
        const resp = await startReview(channelId, {
          pr_number: prNumber,
          mode: m === "review-fix" ? "review-fix" : "review",
          max_iterations: maxIter,
          model: server.session.model ?? "",
          effort: server.session.effort ?? "",
        });
        const started = resp.session;
        if (started) setSession({ ...started, status: "reviewing" });
        // Persist the run id before setLoopRunId so the resync effect (which
        // fires on loopRunId changes) sees the storage entry. Without this the
        // initial mount-and-set would land at the same wall-clock tick as the
        // sessionStorage write, and a competing remount in another browser
        // process could observe an empty key.
        if (typeof window !== "undefined") {
          window.sessionStorage.setItem(reviewRunIdKey(channelId), resp.run_id);
          window.sessionStorage.setItem(reviewDrawerRunIdKey(channelId), resp.run_id);
        }
        broadcastReviewRunIdChange(channelId, resp.run_id);
        // Bind the bottom drawer to this run so its canvas is in front of the
        // user as the loop runs (expanded by default; respects a prior collapse).
        setDrawerRunId(resp.run_id);
        // The workflow.run_started event for this run may have already fired
        // (and been dropped by the chip listener — it returns early when
        // loopRunId is null) before this setState lands. Paint the initial
        // chip here so the chip doesn't sit on "starting..." until the first
        // node_started event arrives in iteration 1.
        setLoopChip("running iter 1");
        setLoopRunId(resp.run_id);
        // Only persist the mode after the run actually launched. A failed
        // session check would otherwise silently flip the user's last-mode
        // (and the primary button label on next mount) even though no run
        // ever started.
        setMode(m);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setSession((prev) => (prev ? { ...prev, status: "ready" } : prev));
        // Show an explicit "failed" chip alongside the error banner instead of
        // clearing it. Clearing made the failure invisible in the header — the
        // user only saw the error text below and had to infer that no run
        // started. A terminal chip mirrors the workflow.run_completed("failed")
        // path so the visual state is consistent across the two failure modes
        // (pre-dispatch throw here vs. daemon-side failure observed via WS).
        setLoopChip("failed");
        setLoopActive(false);
        // Also clear loopRunId in React state. Without this, a setLoopRunId
        // from a *prior* successful run that's still in flight would keep the
        // hydration resync effect firing against a now-stale id. Belt-and-
        // braces with the sessionStorage clear below.
        setLoopRunId(null);
        // No run was dispatched — make sure no stale id from a prior attempt
        // sticks around to fool the next remount's hydration.
        if (typeof window !== "undefined") {
          window.sessionStorage.removeItem(reviewRunIdKey(channelId));
        }
        broadcastReviewRunIdChange(channelId, null);
      } finally {
        setBusy(false);
      }
    },
    [channelId, maxIter],
  );

  const onPrimaryClick = useCallback(() => {
    void runMode(mode);
  }, [runMode, mode]);

  const openModeMenu = useCallback(() => {
    const el = caretRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setMenuPos({ x: r.left, y: r.bottom + 2 });
  }, []);

  const openMoreMenu = useCallback(() => {
    const el = moreBtnRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setMoreMenuPos({ x: r.left, y: r.bottom + 2 });
  }, []);

  // Push the fork choice to the daemon, which stores it on the review
  // session — the Run button dispatches a workflow, so there is no
  // per-run request to hang the option off. Reverts the local draft on
  // failure so the control never shows a choice the daemon didn't take.
  const commitFork = useCallback(
    async (m: ReviewForkMode, sid: string) => {
      setError(null);
      try {
        const resp = await setReviewFork(channelId, m, sid);
        if (resp.present && resp.session) setSession(resp.session);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setForkDraft(session?.fork_session_id ?? "");
      }
    },
    [channelId, session?.fork_session_id],
  );

  // Same storage as the fork choice: the daemon keeps the model/effort on
  // the review session and applies them to the next run.
  const commitAgent = useCallback(
    async (model: string, effort: string) => {
      setError(null);
      try {
        const resp = await setReviewAgent(channelId, model, effort);
        if (resp.present && resp.session) setSession(resp.session);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [channelId],
  );

  const commitScope = useCallback(
    async (scope: ReviewScope) => {
      setError(null);
      try {
        const resp = await setReviewScope(channelId, scope);
        if (resp.present && resp.session) setSession(resp.session);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [channelId],
  );

  const onForkModeChange = useCallback(
    (m: ReviewForkMode) => {
      // "custom" needs an id before the daemon will accept it; hold the
      // selection locally and commit once the user types one in.
      if (m === "custom") {
        setForkModeDraft("custom");
        if (forkDraft.trim()) void commitFork("custom", forkDraft.trim());
        return;
      }
      setForkModeDraft(m);
      void commitFork(m, "");
    },
    [commitFork, forkDraft],
  );

  const onSync = useCallback(async () => {
    setBusy(true);
    setError(null);
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
    setBusy(true);
    setError(null);
    try {
      await deleteReviewSession(channelId);
      setSession(null);
      // The worktree is gone — drop the canvas drawer for this channel too.
      setDrawerRunId(null);
      if (typeof window !== "undefined") {
        window.sessionStorage.removeItem(reviewDrawerRunIdKey(channelId));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId]);

  const onPushOne = useCallback(
    async (c: ReviewComment) => {
      setError(null);
      try {
        await pushReviewComment(channelId, c.id);
        setSession((prev) =>
          prev
            ? {
                ...prev,
                comments: prev.comments.map((x) => (x.id === c.id ? { ...x, pushed: true } : x)),
              }
            : prev,
        );
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [channelId],
  );

  const onDeleteOne = useCallback(
    async (c: ReviewComment) => {
      // Confirm in-line: deleting a pushed/github comment removes it from
      // the PR on GitHub too, which is irreversible — but for unpushed
      // local agent comments it's just dropping a draft, so the prompt
      // shouldn't be alarmist. Keep one prompt with a wording that adapts
      // to the situation.
      const hitsGitHub = !!c.github_id;
      const msg = hitsGitHub ? "Delete this comment from GitHub? This cannot be undone." : "Discard this comment?";
      if (!window.confirm(msg)) return;
      setError(null);
      try {
        await deleteReviewComment(channelId, c.id);
        setSession((prev) =>
          prev
            ? {
                ...prev,
                comments: prev.comments.filter((x) => x.id !== c.id),
              }
            : prev,
        );
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [channelId],
  );

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

  // Discuss stops at the composer: it opens the chat beside the diff, drops a
  // quoted copy of the finding in, and leaves both the question and the
  // decision to send to the user.
  const onDiscussOne = useCallback(
    (c: ReviewComment) => {
      ensureChatOpen();
      window.dispatchEvent(
        new CustomEvent("loop:chat-compose", {
          detail: { channelId, text: buildDiscussDraft(c) },
        }),
      );
    },
    [channelId, ensureChatOpen],
  );

  // Why? and Address both send. There is nothing left to type in either, so
  // stopping at the composer would only cost a click — Discuss is still there
  // for the times the wording needs work.
  const sendAboutComment = useCallback(
    async (prompt: string) => {
      setError(null);
      try {
        ensureChatOpen();
        await sendMessage(channelId, prompt);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [channelId, ensureChatOpen],
  );

  const onWhyOne = useCallback((c: ReviewComment) => sendAboutComment(buildDiscussDraft(c, WHY_QUESTION)), [sendAboutComment]);
  const onAddressOne = useCallback((c: ReviewComment) => sendAboutComment(buildAddressPrompt(c, session)), [sendAboutComment, session]);

  // Address all, or with blockingOnly the blocking findings alone.
  const onAddressAll = useCallback(
    async (blockingOnly = false) => {
      const pending = (session?.comments ?? []).filter(blockingOnly ? isBlocking : isActionable);
      if (pending.length === 0) return;
      // Toggle `busy` for the duration of the sendMessage round-trip so the
      // header buttons (which all gate on `busy`) actually disable. Without
      // this the disabled={busy} on "Address all" was a no-op and
      // double-clicks queued duplicate prompts to the agent.
      setBusy(true);
      setError(null);
      try {
        ensureChatOpen();
        const prompt = buildAddressAllPrompt(pending, session);
        await sendMessage(channelId, prompt);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(false);
      }
    },
    [channelId, ensureChatOpen, session],
  );

  const newCount = useMemo(() => (session?.comments ?? []).filter((c) => c.source !== "github").length, [session]);

  const onCheckAll = useCallback(async () => {
    if (newCount === 0) return;
    setBusy(true);
    setError(null);
    try {
      ensureChatOpen();
      await sendMessage(channelId, buildCheckAllPrompt(session));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId, ensureChatOpen, session, newCount]);

  // Stop cancels the loop before the review: a loop left running would start
  // its next round as soon as the stopped review is ready again. Between
  // reviews (a fix step) only the loop is running.
  const onStop = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      if (loopActive && loopRunId) await cancelWorkflowRun(loopRunId);
      if (reviewRunning) await stopReview(channelId);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [channelId, loopActive, loopRunId, reviewRunning]);

  // Push all, or with blockingOnly the blocking findings alone.
  const onPushAll = useCallback(
    async (blockingOnly = false) => {
      setBusy(true);
      setError(null);
      try {
        const result = await pushAllReviewComments(channelId, blockingOnly ? { impact: "blocking" } : {});
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
    },
    [channelId],
  );

  const pendingCount = useMemo(() => (session?.comments ?? []).filter(isActionable).length, [session]);
  const blockingCount = useMemo(() => (session?.comments ?? []).filter(isBlocking).length, [session]);
  const skipped = useMemo(() => skippedNote(session?.comments ?? []), [session]);

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

  // An icon-only button: a larger glyph, with the line height pinned so the
  // button stays as tall as its text neighbours.
  const glyphBtnStyle: React.CSSProperties = { fontSize: 15, lineHeight: "13px", padding: "4px 8px" };

  // Disabled styling is applied inline so the button visibly dims and
  // shows a not-allowed cursor — otherwise `disabled` is a no-op visual.
  const disabledStyle: React.CSSProperties = { opacity: 0.4, cursor: "not-allowed" };
  const syncDisabled = busy || reviewRunning || session?.status === "loading";
  // Address all and Push all wait for the verification step's verdicts.
  const allDisabled = busy || verifying;
  // `loopActive` is the single source of truth for "a run is in flight": set
  // when runMode dispatches the workflow, cleared in the workflow.run_completed
  // handler regardless of the daemon's terminal status. Earlier this gate also
  // checked `loopRunId !== null && !isLoopTerminal` as defense-in-depth, but
  // `isLoopTerminal` only matched the three known terminal chip strings
  // ("done"/"failed"/"cancelled"). Any other terminal status from the daemon
  // (e.g. "deleted" from engine.DeleteRun, or anything added later) left
  // isLoopTerminal false and trapped the Run button until the user reloaded.
  const runDisabled = busy || session?.status !== "ready" || loopActive;
  const statsBtn = (
    <button
      data-testid="review-stats-btn"
      onClick={() => setShowStats((v) => !v)}
      aria-pressed={showStats}
      style={showStats ? primaryBtnStyle : btnStyle}
      title={showStats ? "Back to the review" : "Review stats of this repo: runs, findings, verdicts and what became of them"}
    >
      Stats
    </button>
  );
  const closeDisabled = busy;
  const approveDisabled = busy || reviewRunning || !outcome || !canApprove(outcome);
  // Approving past the outcome: offered when the plain button is held back
  // by the outcome alone.
  const approveAnyway = !!session && !!outcome && !canApprove(outcome) && !isApprovedAtHead(session) && !busy && !reviewRunning;
  const openApprove = (force: boolean) => {
    if (!session || !outcome) return;
    setApproveDraft({ body: approveBody(outcome, session.comments), force });
  };
  const onApprove = async (body: string) => {
    if (!approveDraft) return;
    const sha = await approveReview(channelId, { body, force: approveDraft.force });
    setSession((prev) => (prev ? { ...prev, approved_sha: sha } : prev));
    setApproveDraft(null);
  };
  const settingsSummary = runSettingsSummary({
    maxIter,
    model: session?.model ?? "",
    effort: session?.effort ?? "",
    forkMode: forkModeDraft,
    scope: session?.scope ?? "",
  });

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
          <>
            <div style={{ flex: 1, fontSize: 12, color: colors.textDim, fontFamily: fonts.sans }}>{showStats ? "Review stats" : "Select a PR to review"}</div>
            {statsBtn}
          </>
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
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{session.pr.title ?? ""}</span>
                  </a>
                ) : (
                  <>
                    <span style={{ fontFamily: "monospace" }}>#{session?.pr?.number}</span>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{session?.pr?.title ?? ""}</span>
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
                {" · "}
                {session ? statusLabel(session.status) : ""}
                {reviewScopeChip(session) && (
                  <span data-testid="review-scope-chip" title="What the latest review run reviewed">
                    {" · last run: "}
                    {reviewScopeChip(session)}
                  </span>
                )}
                {loopChip && (
                  <span
                    data-testid="review-loop-chip"
                    style={{
                      marginLeft: 6,
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
              </div>
            </div>
            {outcome && (
              <span
                data-testid="review-outcome-chip"
                data-state={outcome.state}
                style={{
                  fontSize: 10,
                  fontFamily: fonts.sans,
                  color: outcomeColor(colors, outcome.state),
                  padding: "2px 6px",
                  border: `1px solid ${outcomeColor(colors, outcome.state)}`,
                  borderRadius: 10,
                  whiteSpace: "nowrap",
                }}
                title={outcomeTitle(outcome)}
              >
                {outcomeLabel(outcome.state)}
              </span>
            )}
            {session && isApprovedAtHead(session) ? (
              <span
                data-testid="review-approved-chip"
                style={{ fontSize: 10, fontFamily: fonts.sans, color: colors.active, whiteSpace: "nowrap" }}
                title={`Approved on GitHub at ${session.approved_sha}`}
              >
                ✓ Approved at <span style={{ fontFamily: "monospace" }}>{session.approved_sha?.slice(0, 7)}</span>
              </span>
            ) : (
              <button
                data-testid="review-approve-btn"
                onClick={() => openApprove(false)}
                disabled={approveDisabled}
                style={approveDisabled ? { ...btnStyle, ...disabledStyle } : { ...btnStyle, color: colors.text }}
                title={
                  outcome && !canApprove(outcome)
                    ? approveDisabledReason(outcome)
                    : reviewRunning
                      ? "Wait for the review to finish"
                      : `Approve the PR on GitHub at ${session?.head_sha?.slice(0, 7) ?? "its head"}`
                }
              >
                Approve on GitHub
              </button>
            )}
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
                title={reviewRunning ? "Review already running" : `Start ${mode === "review-fix" ? "review + fix loop" : "review loop"}`}
              >
                {reviewRunning ? "Running..." : modePrimaryLabel(mode)}
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
            {(reviewRunning || loopActive) && (
              <button
                data-testid="review-stop-btn"
                onClick={() => void onStop()}
                disabled={busy}
                style={busy ? { ...btnStyle, ...disabledStyle } : { ...btnStyle, color: colors.dangerText }}
                title={loopActive ? "Stop the review loop and the review in progress" : "Stop the review in progress"}
              >
                Stop
              </button>
            )}
            <button
              data-testid="review-settings-btn"
              onClick={() => setShowSettings((v) => !v)}
              aria-pressed={showSettings}
              aria-label="Run settings"
              style={{ ...(showSettings ? primaryBtnStyle : btnStyle), ...glyphBtnStyle }}
              title={showSettings ? "Hide the run settings" : `Run settings: ${settingsSummary}`}
            >
              ⚙
            </button>
            <button
              ref={moreBtnRef}
              data-testid="review-more-btn"
              onClick={openMoreMenu}
              aria-haspopup="menu"
              aria-label="More review actions"
              style={btnStyle}
              title="Sync, the All actions, Stats and Close"
            >
              ⋯
            </button>
          </>
        )}
      </div>

      {/* Run settings, shown on demand from the header's gear */}
      {hasSession && showSettings && (
        <div
          data-testid="review-settings-row"
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: 6,
            padding: "4px 8px",
            borderBottom: `1px solid ${colors.border}`,
            alignItems: "center",
            fontSize: 10,
            color: colors.textDim,
            fontFamily: fonts.sans,
          }}
        >
          <span>Iterations</span>
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
            aria-label="Maximum review iterations (1-10)"
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
          <span>Model</span>
          <select
            data-testid="review-model"
            value={session?.model ?? ""}
            onChange={(e) => void commitAgent(e.target.value, session?.effort ?? "")}
            disabled={runDisabled}
            aria-label="Model the review run uses"
            title="Model the review run uses. Default follows the config's claude_model."
            style={{
              background: "transparent",
              color: colors.text,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              padding: "3px 4px",
              fontSize: 11,
              fontFamily: fonts.sans,
            }}
          >
            {reviewModelOptions(session?.model ?? "", agentDefaults.model, agentDefaults.models).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <span>Effort</span>
          <select
            data-testid="review-effort"
            value={session?.effort ?? ""}
            onChange={(e) => void commitAgent(session?.model ?? "", e.target.value)}
            disabled={runDisabled}
            aria-label="Reasoning effort the review run uses"
            title="Reasoning effort the review run uses. high, xhigh or max are recommended for reviews; lower levels miss findings."
            style={{
              background: "transparent",
              color: colors.text,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              padding: "3px 4px",
              fontSize: 11,
              fontFamily: fonts.sans,
            }}
          >
            {reviewEffortOptions(agentDefaults.effort).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <span>Session</span>
          <select
            data-testid="review-fork-mode"
            value={forkModeDraft}
            onChange={(e) => onForkModeChange(e.target.value as ReviewForkMode)}
            disabled={runDisabled}
            aria-label="Session the review run forks from"
            title="Which Claude session the review run starts from. Forking gives the reviewer the chat's context; it never appends to the chat session."
            style={{
              background: "transparent",
              color: colors.text,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              padding: "3px 4px",
              fontSize: 11,
              fontFamily: fonts.sans,
            }}
          >
            <option value="current">Fork chat session</option>
            <option value="">Fresh session</option>
            <option value="custom">Fork session id...</option>
          </select>
          <span>Scope</span>
          <select
            data-testid="review-scope"
            value={session?.scope ?? ""}
            onChange={(e) => void commitScope(e.target.value as ReviewScope)}
            disabled={runDisabled}
            aria-label="What the review run reviews"
            title="What the review run reviews. Auto reviews only the commits since the last review when an earlier run finished and its head is still in the branch, else the whole PR."
            style={{
              background: "transparent",
              color: colors.text,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              padding: "3px 4px",
              fontSize: 11,
              fontFamily: fonts.sans,
            }}
          >
            {REVIEW_SCOPE_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          {forkModeDraft === "custom" && (
            <input
              data-testid="review-fork-session-id"
              value={forkDraft}
              onChange={(e) => setForkDraft(e.target.value)}
              onBlur={() => {
                const v = forkDraft.trim();
                if (v && v !== session?.fork_session_id) void commitFork("custom", v);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") e.currentTarget.blur();
              }}
              disabled={runDisabled}
              placeholder="session id"
              aria-label="Claude session id to fork"
              title="Claude session id to fork (from ~/.claude/projects/<project>/<id>.jsonl)"
              style={{
                width: 120,
                background: "transparent",
                color: colors.text,
                border: `1px solid ${session?.fork_session_id ? colors.border : colors.dangerText}`,
                borderRadius: 4,
                padding: "3px 4px",
                fontSize: 11,
                fontFamily: fonts.mono,
              }}
            />
          )}
        </div>
      )}

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
            renders the same card and the panel chip points the user there.
            Pick whatever source has a pending approval (chat, agent, etc.) —
            limiting to the literal "chat" key would silently hide approvals
            from any future router that emits a non-chat source. */}
        {(() => {
          if (hasChatPanel) return null;
          const entries = Object.entries(gateApprovals ?? {});
          if (entries.length === 0) return null;
          const [source, data] = entries[0]!;
          return (
            <div data-testid="review-inline-approval" style={{ padding: 8, borderBottom: `1px solid ${colors.border}` }}>
              <ApprovalCard data={data} channelId={channelId} onResolved={() => onClearGateApproval?.(source)} />
            </div>
          );
        })()}
        {showStats && <ReviewStatsView channelId={channelId} prNumber={hasSession ? session?.pr?.number : undefined} colors={colors} />}
        {!showStats && !hasSession && <PRListPicker prs={prList} loading={listLoading} loadingPR={loadingPR} disabled={busy} colors={colors} onSelect={onSelectPR} />}
        {!showStats && hasSession && session && session.status === "reviewing" && session.comments.length === 0 && (
          <div style={{ padding: "6px 12px", color: colors.textDim, fontSize: 11, borderBottom: `1px solid ${colors.border}` }}>Reviewing... comments will appear inline as the agent emits them.</div>
        )}
        {!showStats && hasSession && session && (
          <ReviewDiffView
            channelId={channelId}
            rawDiff={session.raw_diff ?? ""}
            comments={threadedComments}
            reviewRuns={session.review_runs}
            worktreePath={session.worktree_path}
            onPushComment={onPushOne}
            onDiscussComment={onDiscussOne}
            onWhyComment={onWhyOne}
            onAddressComment={onAddressOne}
            onDeleteComment={onDeleteOne}
          />
        )}
      </div>

      {/* Bottom drawer: live workflow canvas for the active/last review run,
          so the user can watch the loop's nodes without opening the Workflows
          panel. Only shown once a run has been kicked off on this session. */}
      {hasSession && drawerRunId && <ReviewRunDrawer runId={drawerRunId} colors={colors} subscribeChatEvents={subscribeChatEvents} collapsed={drawerCollapsed} onToggleCollapsed={onToggleDrawer} />}
      {menuPos && (
        <ContextMenu
          x={menuPos.x}
          y={menuPos.y}
          onClose={() => setMenuPos(null)}
          items={[
            {
              // Re-check runDisabled here: the caret guards menu-open, but a
              // run can flip active between menu-open and item-click (e.g. a
              // loop kicked off via a peer mount or sessionStorage rehydrate).
              // Without the guard, clicking an item would start a second
              // overlapping workflow run on the same channel.
              label: "Run review (one-shot)",
              onClick: () => {
                if (!runDisabled) void runMode("review-only");
              },
            },
            {
              label: "Run review + fix loop",
              onClick: () => {
                if (!runDisabled) void runMode("review-fix");
              },
            },
          ]}
        />
      )}
      {moreMenuPos && (
        <ContextMenu
          x={moreMenuPos.x}
          y={moreMenuPos.y}
          onClose={() => setMoreMenuPos(null)}
          items={moreMenuItems({
            syncDisabled,
            // Address all and Push all wait for the verification's verdicts.
            all: allDisabled
              ? []
              : allActionItems({
                  newCount,
                  pendingCount,
                  blockingCount,
                  skipped,
                  busy: allDisabled,
                  onCheckAll: () => void onCheckAll(),
                  onAddressAll: () => void onAddressAll(),
                  onPushAll: () => void onPushAll(),
                  onAddressBlocking: () => void onAddressAll(true),
                  onPushBlocking: () => void onPushAll(true),
                }),
            approveAnyway,
            showStats,
            closeDisabled,
            onSync: () => void onSync(),
            onApproveAnyway: () => openApprove(true),
            onToggleStats: () => setShowStats((v) => !v),
            onClose: () => void onCloseSession(),
          })}
        />
      )}
      {approveDraft && session && (
        <ReviewApproveDialog
          prNumber={session.pr?.number}
          headSha={session.head_sha}
          initialBody={approveDraft.body}
          force={approveDraft.force}
          warning={outcome ? approveDisabledReason(outcome).split(";")[0] : undefined}
          onCancel={() => setApproveDraft(null)}
          onSubmit={onApprove}
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
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>{pr.title ?? ""}</span>
            {loadingPR === pr.number && <span style={{ fontSize: 10, color: colors.textDim }}>loading...</span>}
          </div>
          <div style={{ fontSize: 10, color: colors.textDim }}>
            {pr.base_ref} ← {pr.head_ref}
          </div>
        </button>
      ))}
    </div>
  );
}
