import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchAgentConfig, sendMessage } from "../../api/channels";
import {
  approveReview,
  deleteReviewComment,
  deleteReviewSession,
  getReviewSession,
  listReviewPRs,
  loadReviewPR,
  type PushAllResult,
  pushAllReviewComments,
  pushReviewComment,
  type ReviewComment,
  type ReviewPR,
  type ReviewSession,
  type ReviewSessionResponse,
  setReviewAgent,
  setReviewFork,
  setReviewScope,
  startReview,
  stopReview,
  syncReviewSession,
} from "../../api/review";
import { cancelWorkflowRun, FetchWorkflowRunError, fetchWorkflowRun, fetchWorkflowRuns } from "../../api/workflows";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { GateApprovalRequestedData, WSEvent } from "../../types";
import { buildAddressAllPrompt, buildAddressPrompt, buildCheckAllPrompt, buildDiscussDraft, ReviewPanel, WHY_QUESTION } from "./ReviewPanel";

vi.mock("../../api/review", () => ({
  approveReview: vi.fn(),
  deleteReviewComment: vi.fn(),
  deleteReviewSession: vi.fn(),
  getReviewSession: vi.fn(),
  getReviewStats: vi.fn(),
  listReviewPRs: vi.fn(),
  loadReviewPR: vi.fn(),
  pushAllReviewComments: vi.fn(),
  pushReviewComment: vi.fn(),
  setReviewAgent: vi.fn(),
  setReviewFork: vi.fn(),
  setReviewScope: vi.fn(),
  startReview: vi.fn(),
  stopReview: vi.fn(),
  syncReviewSession: vi.fn(),
}));

vi.mock("../../api/channels", () => ({
  fetchAgentConfig: vi.fn(),
  sendMessage: vi.fn(),
}));

vi.mock("../../api/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/workflows")>()),
  cancelWorkflowRun: vi.fn(),
  fetchWorkflowRun: vi.fn(),
  fetchWorkflowRuns: vi.fn(),
}));

// ReviewDiffView parses the diff and lays out comment cards with observers
// jsdom lacks; the stub lists each comment with the card's actions, wired to
// the callbacks the panel hands it, and shows the push state the panel puts
// in ReviewPushContext.
vi.mock("./ReviewDiffView", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./ReviewDiffView")>();
  const { useContext } = await import("react");
  return {
    ...actual,
    ReviewDiffView: ({
      rawDiff,
      comments,
      onPushComment,
      onDeleteComment,
      onAddressComment,
      onWhyComment,
      onDiscussComment,
    }: {
      rawDiff: string;
      comments: ReviewComment[];
      onPushComment: (c: ReviewComment) => void;
      onDeleteComment: (c: ReviewComment) => void;
      onAddressComment: (c: ReviewComment) => void;
      onWhyComment: (c: ReviewComment) => void;
      onDiscussComment: (c: ReviewComment) => void;
    }) => {
      const push = useContext(actual.ReviewPushContext);
      return (
        <ul data-testid="diff-stub" data-raw-diff={rawDiff}>
          {comments.map((c) => (
            <li key={c.id} data-testid={`comment-${c.id}`} data-pushed={String(c.pushed)} data-push-state={actual.commentPushState(c.id, push) ?? ""}>
              {c.body}
              <button onClick={() => onPushComment(c)}>Push {c.id}</button>
              <button onClick={() => onDeleteComment(c)}>Delete {c.id}</button>
              <button onClick={() => onAddressComment(c)}>Address {c.id}</button>
              <button onClick={() => onWhyComment(c)}>Why {c.id}</button>
              <button onClick={() => onDiscussComment(c)}>Discuss {c.id}</button>
            </li>
          ))}
        </ul>
      );
    },
  };
});

// The drawer draws the run's workflow graph; the stub says which run and
// whether it's collapsed, and offers the collapse toggle.
vi.mock("./ReviewRunDrawer", () => ({
  ReviewRunDrawer: ({ runId, collapsed, onToggleCollapsed }: { runId: string; collapsed: boolean; onToggleCollapsed: () => void }) => (
    <div data-testid="run-drawer" data-collapsed={String(collapsed)}>
      {runId}
      <button type="button" aria-label="Toggle drawer" onClick={onToggleCollapsed} />
    </div>
  ),
}));

// The stats view fetches and lays out its own tables; the stub says which PR
// it was asked for.
vi.mock("./ReviewStatsView", () => ({
  ReviewStatsView: ({ prNumber }: { prNumber?: number }) => <div data-testid="stats-stub">{String(prNumber)}</div>,
}));

// The approval card talks to the gate API; the stub shows the gate and
// resolves it.
vi.mock("../chat/ApprovalCard", () => ({
  ApprovalCard: ({ data, onResolved }: { data: GateApprovalRequestedData; onResolved?: () => void }) => (
    <div data-testid="approval-stub">
      {data.req_id}
      <button onClick={onResolved}>Resolve gate</button>
    </div>
  ),
}));

const reviewApi = {
  approve: vi.mocked(approveReview),
  deleteComment: vi.mocked(deleteReviewComment),
  deleteSession: vi.mocked(deleteReviewSession),
  get: vi.mocked(getReviewSession),
  listPRs: vi.mocked(listReviewPRs),
  load: vi.mocked(loadReviewPR),
  pushAll: vi.mocked(pushAllReviewComments),
  pushOne: vi.mocked(pushReviewComment),
  start: vi.mocked(startReview),
  stop: vi.mocked(stopReview),
  sync: vi.mocked(syncReviewSession),
  setAgent: vi.mocked(setReviewAgent),
  setFork: vi.mocked(setReviewFork),
  setScope: vi.mocked(setReviewScope),
};
const sendMock = vi.mocked(sendMessage);
const cancelRunMock = vi.mocked(cancelWorkflowRun);
const fetchRunMock = vi.mocked(fetchWorkflowRun);

const CH = "ch-1";

const prs: ReviewPR[] = [
  { number: 7, url: "https://example.com/pr/7", base_ref: "main", head_ref: "feat/a", state: "open", title: "Add the thing" },
  { number: 9, url: "https://example.com/pr/9", base_ref: "main", head_ref: "feat/b", state: "open", title: "Fix the other", is_draft: true },
];

function comment(id: string, over: Partial<ReviewComment> = {}): ReviewComment {
  return { id, path: "a.go", line: 10, side: "RIGHT", body: `body of ${id}`, pushed: false, source: "agent", ...over };
}

function session(over: Partial<ReviewSession> = {}): ReviewSession {
  return {
    channel_id: CH,
    pr: prs[0],
    head_sha: "abcdef1234567",
    raw_diff: "",
    comments: [comment("c1"), comment("c2", { github_id: 42 })],
    status: "ready",
    updated_at: "2026-11-01T00:00:00Z",
    ...over,
  };
}

const present = (s: ReviewSession): ReviewSessionResponse => ({ present: true, session: s });

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// A chat event hub: the panel subscribes through it, the test emits on it.
function eventHub() {
  const listeners = new Set<ChatEventListener>();
  return {
    subscribe: (l: ChatEventListener) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    emit: (event: WSEvent) => act(() => listeners.forEach((l) => l(event))),
  };
}

function renderPanel(props: Partial<Parameters<typeof ReviewPanel>[0]> = {}) {
  const user = userEvent.setup();
  const hub = eventHub();
  render(<ReviewPanel channelId={CH} subscribeChatEvents={hub.subscribe} {...props} />);
  return { user, hub };
}

// Renders with s as the channel's session and waits for its comments.
async function renderWithSession(s: ReviewSession = session()) {
  reviewApi.get.mockResolvedValue(present(s));
  const r = renderPanel();
  await screen.findByTestId("diff-stub");
  // Flush passive effects (e.g. sessionRef sync) before callers emit WS events.
  await act(async () => {});
  return r;
}

async function openMore(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "More review actions" }));
  return screen.getByRole("menu");
}

describe("ReviewPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.sessionStorage.clear();
    window.localStorage.clear();
    reviewApi.get.mockResolvedValue({ present: false });
    reviewApi.listPRs.mockResolvedValue(prs);
    vi.mocked(fetchAgentConfig).mockResolvedValue({ default_model: "", default_effort: "", models: [] } as never);
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([]);
    fetchRunMock.mockResolvedValue({ run: { status: "running" } } as never);
    sendMock.mockResolvedValue(undefined as never);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("PR picker", () => {
    it("lists the channel's open PRs when there's no session", async () => {
      renderPanel();
      expect(await screen.findByTestId("review-pr-row-7")).toBeTruthy();
      expect(reviewApi.listPRs).toHaveBeenCalledWith(CH);
      expect(screen.getByText("Select a PR to review")).toBeTruthy();
      expect(screen.getByText("Add the thing")).toBeTruthy();
      expect(within(screen.getByTestId("review-pr-row-9")).getByText("draft")).toBeTruthy();
    });

    it("shows a loading line until the list arrives", async () => {
      const list = deferred<ReviewPR[]>();
      reviewApi.listPRs.mockReturnValue(list.promise);
      renderPanel();
      expect(await screen.findByTestId("review-pr-list-loading")).toBeTruthy();
      await act(async () => list.resolve(prs));
      expect(screen.queryByTestId("review-pr-list-loading")).toBeNull();
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });

    it("shows the empty state when the repo has no open PRs", async () => {
      reviewApi.listPRs.mockResolvedValue([]);
      renderPanel();
      expect(await screen.findByTestId("review-pr-list-empty")).toBeTruthy();
    });

    it("shows the error and the empty state when listing fails", async () => {
      reviewApi.listPRs.mockRejectedValue(new Error("gh: not logged in"));
      renderPanel();
      expect((await screen.findByTestId("review-error")).textContent).toBe("gh: not logged in");
      expect(screen.getByTestId("review-pr-list-empty")).toBeTruthy();
    });

    it("shows a failed session fetch as an error", async () => {
      reviewApi.get.mockRejectedValue(new Error("daemon unreachable"));
      renderPanel();
      expect((await screen.findByTestId("review-error")).textContent).toBe("daemon unreachable");
    });

    it("loads the clicked PR and shows it loading, with every row disabled", async () => {
      const load = deferred<ReviewSessionResponse>();
      reviewApi.load.mockReturnValue(load.promise);
      const { user } = renderPanel();
      await user.click(await screen.findByTestId("review-pr-row-7"));
      expect(reviewApi.load).toHaveBeenCalledWith(CH, 7);
      expect(screen.getByText("Loading PR #7…")).toBeTruthy();
      expect(screen.getByTestId("review-pr-row-9")).toHaveProperty("disabled", true);
      await act(async () => load.resolve(present(session({ status: "loading", loading_pr: 7, comments: [] }))));
      // The daemon's loading session keeps the picker marking #7.
      expect(screen.getByText("Loading PR #7…")).toBeTruthy();
      expect(screen.getByTestId("review-cancel-load-btn")).toBeTruthy();
    });

    it("shows a failed load's error and re-enables the rows", async () => {
      reviewApi.load.mockRejectedValue(new Error("worktree busy"));
      const { user } = renderPanel();
      await user.click(await screen.findByTestId("review-pr-row-9"));
      expect((await screen.findByTestId("review-error")).textContent).toBe("worktree busy");
      expect(screen.getByText("Select a PR to review")).toBeTruthy();
      expect(screen.getByTestId("review-pr-row-9")).toHaveProperty("disabled", false);
    });

    it("cancels a PR that is still loading by closing the session", async () => {
      reviewApi.get.mockResolvedValue(present(session({ status: "loading", loading_pr: 7, comments: [] })));
      reviewApi.deleteSession.mockResolvedValue();
      const { user } = renderPanel();
      await user.click(await screen.findByTestId("review-cancel-load-btn"));
      expect(reviewApi.deleteSession).toHaveBeenCalledWith(CH);
      await waitFor(() => expect(screen.getByText("Select a PR to review")).toBeTruthy());
    });
  });

  describe("pushing one comment", () => {
    it("pushes the comment to the PR and marks it pushed", async () => {
      const push = deferred<void>();
      reviewApi.pushOne.mockReturnValue(push.promise);
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Push c1" }));
      expect(reviewApi.pushOne).toHaveBeenCalledWith(CH, "c1");
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("pushing");
      await act(async () => push.resolve());
      const card = screen.getByTestId("comment-c1");
      expect(card.dataset.pushed).toBe("true");
      expect(card.dataset.pushState).toBe("landed");
      expect(screen.getByTestId("comment-c2").dataset.pushed).toBe("false");
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("shows a failed push's error and leaves the comment unpushed", async () => {
      reviewApi.pushOne.mockRejectedValue(new Error("422 line not in diff"));
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Push c1" }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("422 line not in diff");
      const card = screen.getByTestId("comment-c1");
      expect(card.dataset.pushed).toBe("false");
      expect(card.dataset.pushState).toBe("");
    });
  });

  describe("pushing all comments", () => {
    const mixed = () =>
      session({
        comments: [
          comment("c1", { verdict: "real", impact: "blocking" }),
          comment("c2", { verdict: "real", impact: "nice_to_have" }),
          comment("c3", { verdict: "false_positive" }),
          comment("c4", { pushed: true }),
        ],
      });

    it("pushes every actionable comment, refetches, and announces the count", async () => {
      const pushAll = deferred<PushAllResult>();
      reviewApi.pushAll.mockReturnValue(pushAll.promise);
      const { user } = await renderWithSession(mixed());
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Push all to GitHub (2) · skips 1 false positive" }));
      expect(reviewApi.pushAll).toHaveBeenCalledWith(CH, {});
      expect(screen.getByTestId("review-push-progress").textContent).toBe("Pushing to GitHub… 0 of 2");
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("pushing");
      expect(screen.getByTestId("comment-c3").dataset.pushState).toBe("");

      const after = mixed();
      after.comments = after.comments.map((c) => (c.id === "c1" || c.id === "c2" ? { ...c, pushed: true } : c));
      reviewApi.get.mockResolvedValue(present(after));
      await act(async () => pushAll.resolve({ pushed: 2, skipped: 1, failed: 0 }));

      expect(reviewApi.get).toHaveBeenLastCalledWith(CH);
      expect(screen.getByTestId("review-push-notice").textContent).toBe("✓ Pushed 2 comments to GitHub");
      expect(screen.queryByTestId("review-push-progress")).toBeNull();
      expect(screen.getByTestId("comment-c1").dataset.pushed).toBe("true");
      expect(screen.getByTestId("comment-c2").dataset.pushed).toBe("true");
      expect(screen.getByTestId("comment-c3").dataset.pushed).toBe("false");
    });

    it("pushes only the blocking comments from Push blocking", async () => {
      reviewApi.pushAll.mockResolvedValue({ pushed: 1, skipped: 0, failed: 0 });
      const { user } = await renderWithSession(mixed());
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Push blocking to GitHub (1)" }));
      expect(reviewApi.pushAll).toHaveBeenCalledWith(CH, { impact: "blocking" });
      expect(await screen.findByTestId("review-push-notice")).toHaveProperty("textContent", "✓ Pushed 1 comment to GitHub");
    });

    it("reports the comments that failed to push", async () => {
      reviewApi.pushAll.mockResolvedValue({ pushed: 1, skipped: 0, failed: 1, errors: ["c2: 422 line not in diff"] });
      const { user } = await renderWithSession(mixed());
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: /^Push all to GitHub/ }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("1 comment(s) failed to push: c2: 422 line not in diff");
      expect(screen.getByTestId("review-push-notice").textContent).toBe("✓ Pushed 1 comment to GitHub");
    });

    it("shows a rejected Push all's error and clears the in-flight state", async () => {
      reviewApi.pushAll.mockRejectedValue(new Error("GitHub rate limit"));
      const { user } = await renderWithSession(mixed());
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: /^Push all to GitHub/ }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("GitHub rate limit");
      expect(screen.queryByTestId("review-push-progress")).toBeNull();
      expect(screen.queryByTestId("review-push-notice")).toBeNull();
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("");
      expect(screen.getByTestId("comment-c1").dataset.pushed).toBe("false");
    });

    it("offers no All actions while the verification step runs", async () => {
      const { user } = await renderWithSession({ ...mixed(), status: "verifying" });
      const menu = await openMore(user);
      expect(within(menu).queryByRole("menuitem", { name: /Push all/ })).toBeNull();
      expect(within(menu).queryByRole("menuitem", { name: /Address all/ })).toBeNull();
    });
  });

  describe("deleting a comment", () => {
    it.each([
      ["c1", "Discard this comment?"],
      ["c2", "Delete this comment from GitHub? This cannot be undone."],
    ])("deletes %s after confirming %j", async (id, question) => {
      const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
      reviewApi.deleteComment.mockResolvedValue();
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: `Delete ${id}` }));
      expect(confirm).toHaveBeenCalledWith(question);
      expect(reviewApi.deleteComment).toHaveBeenCalledWith(CH, id);
      await waitFor(() => expect(screen.queryByTestId(`comment-${id}`)).toBeNull());
      expect(screen.getAllByRole("listitem")).toHaveLength(1);
    });

    it("does nothing when the user cancels the confirm", async () => {
      vi.spyOn(window, "confirm").mockReturnValue(false);
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Delete c2" }));
      expect(reviewApi.deleteComment).not.toHaveBeenCalled();
      expect(screen.getByTestId("comment-c2")).toBeTruthy();
    });

    it("keeps the comment and shows the error when the delete fails", async () => {
      vi.spyOn(window, "confirm").mockReturnValue(true);
      reviewApi.deleteComment.mockRejectedValue(new Error("403 forbidden"));
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Delete c2" }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("403 forbidden");
      expect(screen.getByTestId("comment-c2")).toBeTruthy();
    });
  });

  describe("running a review", () => {
    it("re-checks the session, starts the run, and binds the chip and drawer to it", async () => {
      const s = session({ model: "claude-opus", effort: "high" });
      reviewApi.start.mockResolvedValue({ run_id: "run-1" });
      const { user } = await renderWithSession(s);
      const run = screen.getByTestId("review-run-btn");
      expect(run.textContent).toBe("Run review");
      await user.click(run);
      expect(reviewApi.start).toHaveBeenCalledWith(CH, { pr_number: 7, mode: "review", max_iterations: 3, model: "claude-opus", effort: "high" });
      expect((await screen.findByTestId("review-loop-chip")).textContent).toBe("running iter 1");
      expect(screen.getByTestId("run-drawer").textContent).toBe("run-1");
      expect(window.sessionStorage.getItem(`loop.review.activeRunId.${CH}`)).toBe("run-1");
      expect(screen.getByTestId("review-run-btn").textContent).toBe("Running...");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", true);
      await waitFor(() => expect(fetchRunMock).toHaveBeenCalledWith("run-1", expect.anything()));
    });

    it("starts a review + fix loop from the mode menu, with the edited pass count", async () => {
      reviewApi.start.mockResolvedValue({ run_id: "run-2" });
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Run settings" }));
      const iters = screen.getByRole("spinbutton", { name: "Maximum review iterations (1-10)" });
      // Replace the 3 in place: clearing first would leave a blank the
      // input ignores, and the 5 would append to the 3.
      await user.type(iters, "5", { initialSelectionStart: 0, initialSelectionEnd: 1 });
      await user.click(screen.getByRole("button", { name: "Choose review run mode" }));
      await user.click(screen.getByRole("menuitem", { name: "Run review + fix loop" }));
      expect(reviewApi.start).toHaveBeenCalledWith(CH, expect.objectContaining({ mode: "review-fix", max_iterations: 5 }));
      await waitFor(() => expect(window.localStorage.getItem("loop.review.lastMode")).toBe("review-fix"));
    });

    it("shows a failed start's error, marks the chip failed and re-enables Run", async () => {
      reviewApi.start.mockRejectedValue(new Error("worktree dirty"));
      const { user } = await renderWithSession();
      await user.click(screen.getByTestId("review-run-btn"));
      expect((await screen.findByTestId("review-error")).textContent).toBe("worktree dirty");
      expect(screen.getByTestId("review-loop-chip").textContent).toBe("failed");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
      expect(screen.queryByTestId("run-drawer")).toBeNull();
      expect(window.sessionStorage.getItem(`loop.review.activeRunId.${CH}`)).toBeNull();
    });

    it("doesn't start when the daemon has lost the session, and goes back to the picker", async () => {
      const { user } = await renderWithSession();
      reviewApi.get.mockResolvedValue({ present: false });
      await user.click(screen.getByTestId("review-run-btn"));
      expect((await screen.findByTestId("review-error")).textContent).toBe("No review session loaded — pick a PR first, then run the loop.");
      expect(reviewApi.start).not.toHaveBeenCalled();
      expect(await screen.findByTestId("review-pr-list")).toBeTruthy();
    });

    it("paints the chip from the run's workflow events and frees Run when it completes", async () => {
      reviewApi.start.mockResolvedValue({ run_id: "run-1" });
      const { user, hub } = await renderWithSession();
      await user.click(screen.getByTestId("review-run-btn"));
      await screen.findByTestId("run-drawer");
      hub.emit({ type: "workflow.node_started", channel_id: CH, data: { run_id: "run-1", node_id: "review", iteration: 1 } } as WSEvent);
      expect(screen.getByTestId("review-loop-chip").textContent).toBe("review — iter 2");
      hub.emit({ type: "review.status", channel_id: CH, data: { status: "ready" } } as WSEvent);
      hub.emit({ type: "workflow.run_completed", channel_id: CH, data: { run_id: "run-1", status: "completed" } } as WSEvent);
      expect(screen.getByTestId("review-loop-chip").textContent).toBe("done");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
      expect(window.sessionStorage.getItem(`loop.review.activeRunId.${CH}`)).toBeNull();
      // The drawer keeps the finished run's canvas.
      expect(screen.getByTestId("run-drawer").textContent).toBe("run-1");
    });

    it("adopts a review run already live for the channel on mount", async () => {
      vi.mocked(fetchWorkflowRuns).mockResolvedValue([{ id: "run-9", workflow_name: "review-fix-loop", status: "running" }] as never);
      await renderWithSession();
      expect(await screen.findByTestId("run-drawer")).toHaveProperty("textContent", "run-9");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", true);
      expect(screen.getByTestId("review-stop-btn")).toBeTruthy();
    });
  });

  describe("stopping", () => {
    it("stops the review in progress", async () => {
      reviewApi.stop.mockResolvedValue();
      const { user } = await renderWithSession(session({ status: "reviewing" }));
      await user.click(screen.getByTestId("review-stop-btn"));
      expect(reviewApi.stop).toHaveBeenCalledWith(CH);
      expect(cancelRunMock).not.toHaveBeenCalled();
    });

    it("cancels the loop before stopping the review", async () => {
      window.sessionStorage.setItem(`loop.review.activeRunId.${CH}`, "run-3");
      cancelRunMock.mockResolvedValue(undefined as never);
      reviewApi.stop.mockResolvedValue();
      const { user } = await renderWithSession(session({ status: "reviewing" }));
      await user.click(screen.getByTestId("review-stop-btn"));
      await waitFor(() => expect(reviewApi.stop).toHaveBeenCalledWith(CH));
      expect(cancelRunMock).toHaveBeenCalledWith("run-3");
      expect(cancelRunMock.mock.invocationCallOrder[0]).toBeLessThan(reviewApi.stop.mock.invocationCallOrder[0]!);
    });

    it("shows a failed stop's error", async () => {
      reviewApi.stop.mockRejectedValue(new Error("no review running"));
      const { user } = await renderWithSession(session({ status: "reviewing" }));
      await user.click(screen.getByTestId("review-stop-btn"));
      expect((await screen.findByTestId("review-error")).textContent).toBe("no review running");
    });
  });

  describe("sending comments to the chat", () => {
    it("Address sends the address prompt and opens the chat beside the review", async () => {
      const opened = vi.fn();
      window.addEventListener("loop:open-panel", opened);
      const s = session();
      const { user } = await renderWithSession(s);
      await user.click(screen.getByRole("button", { name: "Address c1" }));
      expect(sendMock).toHaveBeenCalledWith(CH, buildAddressPrompt(s.comments[0]!, s));
      expect((opened.mock.calls[0]![0] as CustomEvent).detail).toEqual({ channelId: CH, panel: "chat", anchorPanel: "review" });
      window.removeEventListener("loop:open-panel", opened);
    });

    it("Why? sends the quoted comment with the canned question", async () => {
      const s = session();
      const { user } = await renderWithSession(s);
      await user.click(screen.getByRole("button", { name: "Why c2" }));
      expect(sendMock).toHaveBeenCalledWith(CH, buildDiscussDraft(s.comments[1]!, WHY_QUESTION));
    });

    it("Discuss drops the quote in the composer without sending", async () => {
      const composed = vi.fn();
      window.addEventListener("loop:chat-compose", composed);
      const s = session();
      const { user } = await renderWithSession(s);
      await user.click(screen.getByRole("button", { name: "Discuss c1" }));
      expect((composed.mock.calls[0]![0] as CustomEvent).detail).toEqual({ channelId: CH, text: buildDiscussDraft(s.comments[0]!) });
      expect(sendMock).not.toHaveBeenCalled();
      window.removeEventListener("loop:chat-compose", composed);
    });

    it("shows a failed send's error", async () => {
      sendMock.mockRejectedValue(new Error("agent offline"));
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Address c1" }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("agent offline");
    });

    it("Address all sends the actionable comments in one prompt", async () => {
      const s = session({ comments: [comment("c1"), comment("c2", { verdict: "already_fixed" }), comment("c3")] });
      const { user } = await renderWithSession(s);
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Address all (2) · skips 1 already fixed" }));
      expect(sendMock).toHaveBeenCalledWith(CH, buildAddressAllPrompt([s.comments[0]!, s.comments[2]!], s));
    });
  });

  describe("session actions", () => {
    it("Sync with GitHub takes the synced session", async () => {
      reviewApi.sync.mockResolvedValue(present(session({ comments: [comment("c1"), comment("g1", { source: "github", body: "from GitHub" })] })));
      const { user } = await renderWithSession();
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Sync with GitHub" }));
      expect(reviewApi.sync).toHaveBeenCalledWith(CH);
      expect(await screen.findByTestId("comment-g1")).toBeTruthy();
      expect(screen.queryByTestId("comment-c2")).toBeNull();
    });

    it("shows a failed sync's error and keeps the comments", async () => {
      reviewApi.sync.mockRejectedValue(new Error("sync failed"));
      const { user } = await renderWithSession();
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Sync with GitHub" }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("sync failed");
      expect(screen.getByTestId("comment-c2")).toBeTruthy();
    });

    it("Close review deletes the session and goes back to the picker", async () => {
      reviewApi.deleteSession.mockResolvedValue();
      const { user } = await renderWithSession();
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Close review" }));
      expect(reviewApi.deleteSession).toHaveBeenCalledWith(CH);
      expect(await screen.findByTestId("review-pr-list")).toBeTruthy();
      expect(screen.queryByTestId("diff-stub")).toBeNull();
    });

    it("keeps the review open when closing fails", async () => {
      reviewApi.deleteSession.mockRejectedValue(new Error("worktree locked"));
      const { user } = await renderWithSession();
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Close review" }));
      expect((await screen.findByTestId("review-error")).textContent).toBe("worktree locked");
      expect(screen.getByTestId("diff-stub")).toBeTruthy();
    });
  });

  describe("approving", () => {
    // Reviewed at head with only a nice-to-have left: approvable.
    const approvable = () => session({ reviewed_sha: "abcdef1234567", comments: [comment("c1", { verdict: "real", impact: "nice_to_have", body: "rename x" })] });

    it("approves on GitHub with the dialog's text and shows the approved chip", async () => {
      reviewApi.approve.mockResolvedValue("abcdef1234567");
      const { user } = await renderWithSession(approvable());
      await user.click(screen.getByTestId("review-approve-btn"));
      const body = screen.getByTestId("review-approve-body") as HTMLTextAreaElement;
      expect(body.value).toBe("Approved. A few nice-to-haves, none blocking:\n\n- `a.go:10` rename x");
      await user.clear(body);
      await user.type(body, "LGTM");
      await user.click(screen.getByTestId("review-approve-submit"));
      expect(reviewApi.approve).toHaveBeenCalledWith(CH, { body: "LGTM", force: false });
      expect(await screen.findByTestId("review-approved-chip")).toBeTruthy();
      expect(screen.queryByTestId("review-approve-dialog")).toBeNull();
    });

    it("keeps the dialog open with the error when approving fails", async () => {
      reviewApi.approve.mockRejectedValue(new Error("can't approve your own PR"));
      const { user } = await renderWithSession(approvable());
      await user.click(screen.getByTestId("review-approve-btn"));
      await user.click(screen.getByTestId("review-approve-submit"));
      expect((await screen.findByTestId("review-approve-error")).textContent).toBe("can't approve your own PR");
      expect(screen.queryByTestId("review-approved-chip")).toBeNull();
    });

    it("holds Approve back on a blocking finding but offers Approve anyway", async () => {
      reviewApi.approve.mockResolvedValue("abcdef1234567");
      const { user } = await renderWithSession(session({ comments: [comment("c1", { verdict: "real", impact: "blocking" })] }));
      expect(screen.getByTestId("review-approve-btn")).toHaveProperty("disabled", true);
      const menu = await openMore(user);
      await user.click(within(menu).getByRole("menuitem", { name: "Approve anyway…" }));
      expect(screen.getByTestId("review-approve-warning").textContent).toBe("1 blocking comment left");
      await user.click(screen.getByTestId("review-approve-submit"));
      expect(reviewApi.approve).toHaveBeenCalledWith(CH, { body: "", force: true });
    });
  });

  describe("live comment events", () => {
    it("adds, updates and removes comments as the daemon announces them", async () => {
      const { hub } = await renderWithSession();
      hub.emit({ type: "review.comment", channel_id: CH, data: comment("c3", { body: "new finding" }) } as WSEvent);
      expect(screen.getByTestId("comment-c3").textContent).toContain("new finding");
      hub.emit({ type: "review.comment_updated", channel_id: CH, data: comment("c1", { pushed: true }) } as WSEvent);
      expect(screen.getByTestId("comment-c1").dataset.pushed).toBe("true");
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("landed");
      hub.emit({ type: "review.comment_removed", channel_id: CH, data: { id: "c2" } } as WSEvent);
      expect(screen.queryByTestId("comment-c2")).toBeNull();
    });

    it("ignores another channel's events", async () => {
      const { hub } = await renderWithSession();
      hub.emit({ type: "review.comment_removed", channel_id: "other", data: { id: "c2" } } as WSEvent);
      expect(screen.getByTestId("comment-c2")).toBeTruthy();
    });
  });

  describe("prompt builders", () => {
    it("reads a comment without a side as on the new code", () => {
      const c = comment("c1", { side: undefined, author: "octo" });
      expect(buildAddressPrompt(c)).toContain("- Line: 10 (RIGHT — added/new)");
      expect(buildAddressAllPrompt([c])).toContain("### 1. `a.go`:10 (RIGHT — added/new) — @octo");
    });
  });

  describe("mounting", () => {
    it("starts from the last run mode and registers the view for its lifetime", async () => {
      window.localStorage.setItem("loop.review.lastMode", "review-fix");
      const deregister = vi.fn();
      const register = vi.fn(() => deregister);
      reviewApi.get.mockResolvedValue(present(session()));
      const { unmount } = render(<ReviewPanel channelId={CH} registerReviewView={register} />);
      await screen.findByTestId("diff-stub");
      expect(register).toHaveBeenCalledWith(CH);
      expect(screen.getByTestId("review-run-btn").textContent).toBe("Run review + fix");
      unmount();
      expect(deregister).toHaveBeenCalled();
    });

    it("names the config's defaults in the settings, keeping a model the daemon doesn't list", async () => {
      vi.mocked(fetchAgentConfig).mockResolvedValue({ default_model: "claude-opus-4", default_effort: "high" } as never);
      const { user } = await renderWithSession(session({ model: "claude-retired", effort: "max", fork_mode: "current" }));
      await user.click(screen.getByRole("button", { name: "Run settings" }));
      const model = screen.getByRole("combobox", { name: "Model the review run uses" }) as HTMLSelectElement;
      await waitFor(() => expect(within(model).getByRole("option", { name: "Default (opus-4)" })).toBeTruthy());
      expect(model.value).toBe("claude-retired");
      expect(
        within(model)
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toEqual(["Default (opus-4)", "retired"]);
      const effort = screen.getByRole("combobox", { name: "Reasoning effort the review run uses" }) as HTMLSelectElement;
      expect(effort.value).toBe("max");
      expect(within(effort).getByRole("option", { name: "Default (high)" })).toBeTruthy();
      expect((screen.getByRole("combobox", { name: "Session the review run forks from" }) as HTMLSelectElement).value).toBe("current");
    });

    it("ignores a failed agent config fetch", async () => {
      vi.mocked(fetchAgentConfig).mockRejectedValue(new Error("no config"));
      await renderWithSession();
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("drops the answers for a channel it has left", async () => {
      const get1 = deferred<ReviewSessionResponse>();
      const list1 = deferred<ReviewPR[]>();
      const cfg1 = deferred<never>();
      reviewApi.get.mockImplementation((ch) => (ch === CH ? get1.promise : Promise.resolve({ present: false })));
      reviewApi.listPRs.mockImplementation((ch) => (ch === CH ? list1.promise : Promise.resolve([prs[1]!])));
      vi.mocked(fetchAgentConfig).mockImplementation((ch) => (ch === CH ? cfg1.promise : Promise.resolve({ default_model: "", default_effort: "", models: [] } as never)));
      const { rerender } = render(<ReviewPanel channelId={CH} />);
      rerender(<ReviewPanel channelId="ch-2" />);
      expect(await screen.findByTestId("review-pr-row-9")).toBeTruthy();
      await act(async () => {
        get1.resolve(present(session()));
        list1.resolve(prs);
        cfg1.resolve({ default_model: "claude-x", default_effort: "", models: [] } as never);
      });
      expect(screen.queryByTestId("diff-stub")).toBeNull();
      expect(screen.queryByTestId("review-pr-row-7")).toBeNull();
    });

    it("drops a left channel's failures", async () => {
      const get1 = deferred<ReviewSessionResponse>();
      const list1 = deferred<ReviewPR[]>();
      reviewApi.get.mockImplementation((ch) => (ch === CH ? get1.promise : Promise.resolve({ present: false })));
      reviewApi.listPRs.mockImplementation((ch) => (ch === CH ? list1.promise : Promise.resolve(prs)));
      const { rerender } = render(<ReviewPanel channelId={CH} />);
      rerender(<ReviewPanel channelId="ch-2" />);
      await screen.findByTestId("review-pr-list");
      await act(async () => {
        get1.reject(new Error("old get"));
        list1.reject(new Error("old list"));
      });
      expect(screen.queryByTestId("review-error")).toBeNull();
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });

    it("shows a non-Error rejection as text", async () => {
      reviewApi.get.mockRejectedValue("daemon gone");
      reviewApi.listPRs.mockRejectedValue("gh gone");
      renderPanel();
      await waitFor(() => expect(screen.getByTestId("review-error").textContent).toMatch(/^(daemon|gh) gone$/));
      expect(screen.getByTestId("review-pr-list-empty")).toBeTruthy();
    });

    it("works without a chat event source", async () => {
      window.sessionStorage.setItem(`loop.review.activeRunId.${CH}`, "run-1");
      reviewApi.get.mockResolvedValue(present(session()));
      render(<ReviewPanel channelId={CH} />);
      await screen.findByTestId("diff-stub");
      expect(await screen.findByTestId("review-loop-chip")).toHaveProperty("textContent", "running");
    });
  });

  describe("reconnecting", () => {
    it("refetches the session on a reconnect but not on the first open", async () => {
      reviewApi.get.mockResolvedValue(present(session()));
      const { rerender } = render(<ReviewPanel channelId={CH} wsOpens={0} />);
      await screen.findByTestId("diff-stub");
      const calls = reviewApi.get.mock.calls.length;
      rerender(<ReviewPanel channelId={CH} wsOpens={1} />);
      expect(reviewApi.get).toHaveBeenCalledTimes(calls);
      reviewApi.get.mockResolvedValue(present(session({ comments: [comment("c9")] })));
      rerender(<ReviewPanel channelId={CH} wsOpens={2} />);
      expect(await screen.findByTestId("comment-c9")).toBeTruthy();
      reviewApi.get.mockResolvedValue({ present: false });
      rerender(<ReviewPanel channelId={CH} wsOpens={3} />);
      expect(await screen.findByTestId("review-pr-list")).toBeTruthy();
    });

    it("takes only the latest refetch's answer", async () => {
      reviewApi.get.mockResolvedValue(present(session()));
      const { rerender } = render(<ReviewPanel channelId={CH} wsOpens={1} />);
      await screen.findByTestId("diff-stub");
      const first = deferred<ReviewSessionResponse>();
      const second = deferred<ReviewSessionResponse>();
      const third = deferred<ReviewSessionResponse>();
      reviewApi.get.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise).mockReturnValueOnce(third.promise);
      rerender(<ReviewPanel channelId={CH} wsOpens={2} />);
      rerender(<ReviewPanel channelId={CH} wsOpens={3} />);
      rerender(<ReviewPanel channelId={CH} wsOpens={4} />);
      await act(async () => third.resolve(present(session({ comments: [comment("latest")] }))));
      await act(async () => {
        first.resolve(present(session({ comments: [comment("stale")] })));
        second.reject(new Error("stale failure"));
      });
      expect(screen.getByTestId("comment-latest")).toBeTruthy();
      expect(screen.queryByTestId("comment-stale")).toBeNull();
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("shows a failed refetch's error", async () => {
      reviewApi.get.mockResolvedValue(present(session()));
      const { rerender } = render(<ReviewPanel channelId={CH} wsOpens={1} />);
      await screen.findByTestId("diff-stub");
      reviewApi.get.mockRejectedValueOnce(new Error("refetch failed")).mockRejectedValueOnce("refetch gone");
      rerender(<ReviewPanel channelId={CH} wsOpens={2} />);
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "refetch failed");
      rerender(<ReviewPanel channelId={CH} wsOpens={3} />);
      await waitFor(() => expect(screen.getByTestId("review-error").textContent).toBe("refetch gone"));
    });
  });

  describe("runs started in another panel", () => {
    const runKey = `loop.review.activeRunId.${CH}`;
    const custom = (detail?: unknown) => act(() => window.dispatchEvent(new CustomEvent("loop.review.activeRunId.changed", { detail })));
    const storage = (init: StorageEventInit) => act(() => window.dispatchEvent(new StorageEvent("storage", init)));

    it("follows a sibling panel's run through the window event", async () => {
      await renderWithSession();
      custom(undefined);
      custom({ channelId: "other", runId: "run-x" });
      expect(screen.queryByTestId("run-drawer")).toBeNull();
      custom({ channelId: CH, runId: "run-5" });
      expect(screen.getByTestId("run-drawer").textContent).toContain("run-5");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", true);
      expect(screen.getByTestId("review-stop-btn")).toBeTruthy();
      custom({ channelId: CH, runId: null });
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
      expect(screen.queryByTestId("review-stop-btn")).toBeNull();
    });

    it("follows another tab's run through the storage event", async () => {
      await renderWithSession();
      storage({ key: runKey, newValue: "run-x", storageArea: window.localStorage });
      storage({ key: "other-key", newValue: "run-x", storageArea: window.sessionStorage });
      expect(screen.queryByTestId("run-drawer")).toBeNull();
      storage({ key: runKey, newValue: "run-6", storageArea: window.sessionStorage });
      expect(screen.getByTestId("run-drawer").textContent).toContain("run-6");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", true);
      storage({ key: runKey, newValue: null, storageArea: window.sessionStorage });
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
    });

    it("adopts a review run started elsewhere and re-reads the session it loaded", async () => {
      const { hub } = await renderWithSession();
      reviewApi.get.mockResolvedValue(present(session({ comments: [comment("from-run")], fork_mode: "custom", fork_session_id: "sess-1" })));
      hub.emit({ type: "workflow.run_started", channel_id: CH, data: { run_id: "run-7", workflow_name: "review-fix-loop", channel_id: CH } } as WSEvent);
      expect(await screen.findByTestId("comment-from-run")).toBeTruthy();
      expect(screen.getByTestId("run-drawer").textContent).toContain("run-7");
      expect(screen.getByTestId("review-run-btn").textContent).toBe("Run review + fix");
      expect(window.sessionStorage.getItem(runKey)).toBe("run-7");
      const calls = reviewApi.get.mock.calls.length;
      // The same run again is already adopted.
      hub.emit({ type: "workflow.run_started", channel_id: CH, data: { run_id: "run-7", workflow_name: "review-fix-loop", channel_id: CH } } as WSEvent);
      expect(reviewApi.get).toHaveBeenCalledTimes(calls);
    });

    it.each([
      ["no session", () => reviewApi.get.mockResolvedValue({ present: false })],
      ["a failed read", () => reviewApi.get.mockRejectedValue(new Error("read failed"))],
      ["a session without a fork choice", () => reviewApi.get.mockResolvedValue(present(session({ comments: [comment("c1")] })))],
    ])("keeps the held session's PR after a re-read with %s", async (_name, arrange) => {
      const { hub } = await renderWithSession();
      arrange();
      hub.emit({ type: "workflow.run_started", channel_id: CH, data: { run_id: "run-8", workflow_name: "review-loop", channel_id: CH } } as WSEvent);
      expect(await screen.findByTestId("run-drawer")).toHaveProperty("textContent", expect.stringContaining("run-8"));
      expect(screen.getByTestId("diff-stub")).toBeTruthy();
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("ignores a failed run listing", async () => {
      vi.mocked(fetchWorkflowRuns).mockRejectedValue(new Error("runs down"));
      await renderWithSession();
      expect(screen.queryByTestId("run-drawer")).toBeNull();
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("keeps a run adopted while the run listing was on its way", async () => {
      const runs = deferred<never>();
      vi.mocked(fetchWorkflowRuns).mockReturnValue(runs.promise);
      const { hub } = await renderWithSession();
      hub.emit({ type: "workflow.run_started", channel_id: CH, data: { run_id: "run-a", workflow_name: "review-loop", channel_id: CH } } as WSEvent);
      await act(async () => runs.resolve([{ id: "run-b", workflow_name: "review-loop", status: "running" }] as never));
      expect(screen.getByTestId("run-drawer").textContent).toContain("run-a");
    });

    it("drops a run listing that lands after unmount", async () => {
      const runs = deferred<never>();
      vi.mocked(fetchWorkflowRuns).mockReturnValue(runs.promise);
      const { unmount } = render(<ReviewPanel channelId={CH} />);
      unmount();
      await act(async () => runs.resolve([{ id: "run-b", workflow_name: "review-loop", status: "running" }] as never));
      expect(window.sessionStorage.getItem(runKey)).toBeNull();
    });
  });

  describe("resyncing a stored run", () => {
    const runKey = `loop.review.activeRunId.${CH}`;
    const flush = () => act(async () => {});

    it.each([
      ["completed", "done"],
      ["failed", "failed"],
      ["cancelled", "cancelled"],
    ])("clears a run that %s meanwhile and shows %j", async (status, chip) => {
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockResolvedValue({ run: { status } } as never);
      await renderWithSession();
      await waitFor(() => expect(screen.getByTestId("review-loop-chip").textContent).toBe(chip));
      expect(window.sessionStorage.getItem(runKey)).toBeNull();
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
    });

    it.each([
      [true, "paused at gate — see chat"],
      [false, "paused at gate — approve below"],
    ])("points a paused run's gate at the chat (%s) or the panel", async (hasChatPanel, chip) => {
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockResolvedValue({ run: { status: "paused" } } as never);
      reviewApi.get.mockResolvedValue(present(session()));
      renderPanel({ hasChatPanel });
      expect(await screen.findByTestId("review-loop-chip")).toHaveProperty("textContent", chip);
    });

    it("treats a run without a status as still running", async () => {
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockResolvedValue({} as never);
      await renderWithSession();
      expect(await screen.findByTestId("review-loop-chip")).toHaveProperty("textContent", "running");
      expect(window.sessionStorage.getItem(runKey)).toBe("run-1");
    });

    it("drops a run whose row is gone", async () => {
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockRejectedValue(new FetchWorkflowRunError(404, "Not Found"));
      await renderWithSession();
      await waitFor(() => expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false));
      expect(window.sessionStorage.getItem(runKey)).toBeNull();
      expect(screen.queryByTestId("review-loop-chip")).toBeNull();
    });

    it("leaves the run alone when the fetch was aborted", async () => {
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockRejectedValue(new DOMException("aborted", "AbortError"));
      await renderWithSession();
      await flush();
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", true);
      expect(window.sessionStorage.getItem(runKey)).toBe("run-1");
      expect(fetchRunMock).toHaveBeenCalledTimes(1);
    });

    it("retries a transient failure with backoff, then frees Run", async () => {
      vi.useFakeTimers();
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockRejectedValue(new FetchWorkflowRunError(502, "Bad Gateway"));
      reviewApi.get.mockResolvedValue(present(session()));
      render(<ReviewPanel channelId={CH} />);
      await flush();
      expect(fetchRunMock).toHaveBeenCalledTimes(1);
      for (const [ms, calls] of [
        [1000, 2],
        [2000, 3],
        [4000, 4],
      ] as const) {
        await act(async () => vi.advanceTimersByTimeAsync(ms));
        expect(fetchRunMock).toHaveBeenCalledTimes(calls);
      }
      expect(screen.getByTestId("review-loop-chip").textContent).toBe("unknown");
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
    });

    it("keeps the chip it has when retries run out", async () => {
      vi.useFakeTimers();
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockRejectedValue(new TypeError("Failed to fetch"));
      reviewApi.get.mockResolvedValue(present(session()));
      const hub = eventHub();
      render(<ReviewPanel channelId={CH} subscribeChatEvents={hub.subscribe} />);
      await flush();
      hub.emit({ type: "workflow.node_started", channel_id: CH, data: { run_id: "run-1", node_id: "fix", iteration: 0 } } as WSEvent);
      await act(async () => vi.advanceTimersByTimeAsync(7000));
      expect(screen.getByTestId("review-loop-chip").textContent).toBe("fix — iter 1");
    });

    it("stops retrying once unmounted", async () => {
      vi.useFakeTimers();
      window.sessionStorage.setItem(runKey, "run-1");
      fetchRunMock.mockRejectedValue(new Error("hiccup"));
      const { unmount } = render(<ReviewPanel channelId={CH} />);
      await flush();
      unmount();
      await act(async () => vi.advanceTimersByTimeAsync(10000));
      expect(fetchRunMock).toHaveBeenCalledTimes(1);
    });

    it.each([
      ["answer", (d: ReturnType<typeof deferred<never>>) => d.resolve({ run: { status: "completed" } } as never)],
      ["failure", (d: ReturnType<typeof deferred<never>>) => d.reject(new FetchWorkflowRunError(404, "Not Found"))],
    ])("drops an %s that lands after unmount", async (_name, settle) => {
      window.sessionStorage.setItem(runKey, "run-1");
      const d = deferred<never>();
      fetchRunMock.mockReturnValue(d.promise);
      const { unmount } = render(<ReviewPanel channelId={CH} />);
      await flush();
      unmount();
      await act(async () => settle(d));
      expect(window.sessionStorage.getItem(runKey)).toBe("run-1");
    });
  });

  describe("the run chip", () => {
    const runKey = `loop.review.activeRunId.${CH}`;
    const wf = (type: string, data?: Record<string, unknown>) => ({ type, channel_id: CH, data }) as WSEvent;

    async function renderRunning(props: Partial<Parameters<typeof ReviewPanel>[0]> = {}) {
      window.sessionStorage.setItem(runKey, "run-1");
      reviewApi.get.mockResolvedValue(present(session()));
      const r = renderPanel(props);
      await screen.findByTestId("diff-stub");
      await screen.findByTestId("review-loop-chip");
      return r;
    }
    const chip = () => screen.getByTestId("review-loop-chip").textContent;

    it("follows the run's node events and ignores other runs", async () => {
      const { hub } = await renderRunning({ hasChatPanel: true });
      hub.emit(wf("workflow.node_started"));
      hub.emit(wf("workflow.node_started", { run_id: "other", node_id: "review" }));
      expect(chip()).toBe("running");
      hub.emit(wf("workflow.run_started", { run_id: "run-1" }));
      expect(chip()).toBe("running iter 1");
      hub.emit(wf("workflow.node_started", { run_id: "run-1", node_id: "dedup" }));
      expect(chip()).toBe("deduping comments");
      hub.emit(wf("workflow.node_started", { run_id: "run-1", node_id: "check" }));
      expect(chip()).toBe("verifying comments");
      hub.emit(wf("workflow.node_started", { run_id: "run-1", node_id: "fix" }));
      expect(chip()).toBe("fix — iter 1");
      hub.emit(wf("workflow.node_started", { run_id: "run-1" }));
      hub.emit(wf("workflow.node_completed", { run_id: "run-1", node_id: "fix" }));
      hub.emit(wf("workflow.something_else", { run_id: "run-1" }));
      expect(chip()).toBe("fix — iter 1");
      hub.emit(wf("workflow.run_paused", { run_id: "run-1" }));
      expect(chip()).toBe("paused at gate — see chat");
    });

    it("asks for the approval below when no chat panel shows it", async () => {
      const { hub } = await renderRunning({ hasChatPanel: false });
      hub.emit(wf("workflow.run_paused", { run_id: "run-1" }));
      expect(chip()).toBe("paused at gate — approve below");
    });

    it.each([
      ["failed", "failed"],
      ["cancelled", "cancelled"],
      ["deleted", "deleted"],
      [undefined, "done"],
    ])("shows a run that ended %s as %j and frees Run", async (status, text) => {
      const { hub } = await renderRunning();
      hub.emit(wf("workflow.run_completed", { run_id: "run-1", status }));
      expect(chip()).toBe(text);
      expect(screen.getByTestId("review-run-btn")).toHaveProperty("disabled", false);
      expect(window.sessionStorage.getItem(runKey)).toBeNull();
    });

    it("collapses the run drawer and remembers it", async () => {
      window.sessionStorage.setItem(`loop.review.drawerRunId.${CH}`, "run-1");
      window.localStorage.setItem("loop.review.drawerCollapsed", "1");
      const { user } = await renderRunning();
      const drawer = screen.getByTestId("run-drawer");
      expect(drawer.dataset.collapsed).toBe("true");
      await user.click(within(drawer).getByRole("button", { name: "Toggle drawer" }));
      expect(screen.getByTestId("run-drawer").dataset.collapsed).toBe("false");
      expect(window.localStorage.getItem("loop.review.drawerCollapsed")).toBe("0");
    });
  });

  describe("review events", () => {
    const ev = (type: string, data: unknown, channel_id = CH) => ({ type, channel_id, data }) as WSEvent;

    it("ignores comment, status, approval and diff events while no session is open", async () => {
      const { hub } = renderPanel();
      await screen.findByTestId("review-pr-list");
      hub.emit(ev("review.comment", comment("c1")));
      hub.emit(ev("review.approved", { commit: "abc" }));
      hub.emit(ev("review.diff", { raw_diff: "diff" }));
      hub.emit(ev("review.status", { status: "verifying" }));
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
      expect(screen.queryByTestId("diff-stub")).toBeNull();
    });

    it("keeps a comment it already holds and an update that isn't a push", async () => {
      const { hub } = await renderWithSession(session({ comments: [comment("c1"), comment("c2", { pushed: true })] }));
      hub.emit(ev("review.comment", comment("c1", { body: "again" })));
      expect(screen.getByTestId("comment-c1").textContent).toContain("body of c1");
      hub.emit(ev("review.comment_updated", comment("c1", { body: "edited" })));
      expect(screen.getByTestId("comment-c1").textContent).toContain("edited");
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("");
      hub.emit(ev("review.comment_updated", comment("c2", { pushed: true, body: "re-pushed" })));
      expect(screen.getByTestId("comment-c2").dataset.pushState).toBe("");
      hub.emit(ev("review.comment_updated", comment("gone", { pushed: true })));
      expect(screen.queryByTestId("comment-gone")).toBeNull();
    });

    it("refetches the whole session when a PR load starts", async () => {
      const { hub } = await renderWithSession();
      reviewApi.get.mockResolvedValue(present(session({ status: "loading", loading_pr: 9, comments: [] })));
      hub.emit(ev("review.status", { status: "loading" }));
      expect(await screen.findByText("Loading PR #9…")).toBeTruthy();
      expect(reviewApi.get).toHaveBeenLastCalledWith(CH);
    });

    it("takes the run's verdict state when a run settles on the same PR", async () => {
      const { hub } = await renderWithSession();
      reviewApi.get.mockResolvedValue(present(session({ approved_sha: "abcdef1234567" })));
      hub.emit(ev("review.status", { status: "ready" }));
      expect(await screen.findByTestId("review-approved-chip")).toBeTruthy();
      expect(reviewApi.get).toHaveBeenLastCalledWith(CH, { diff: false });
    });

    it.each([
      ["another PR's session", () => present(session({ pr: prs[1], comments: [comment("other-pr")] }))],
      ["no session", () => ({ present: false })],
    ])("refetches whole when a run start brings %s", async (_name, fresh) => {
      const { hub } = await renderWithSession();
      reviewApi.get.mockResolvedValueOnce(fresh() as ReviewSessionResponse).mockResolvedValue(present(session({ pr: prs[1], comments: [comment("other-pr")] })));
      hub.emit(ev("review.status", { status: "reviewing" }));
      expect(await screen.findByTestId("comment-other-pr")).toBeTruthy();
      expect(reviewApi.get).toHaveBeenLastCalledWith(CH);
    });

    it("shows the session's error from a status event, and ignores a failed verdict refresh", async () => {
      const { hub } = await renderWithSession();
      reviewApi.get.mockRejectedValue(new Error("refresh failed"));
      hub.emit(ev("review.status", { status: "verifying", error: "agent crashed" }));
      expect(screen.getByTestId("review-error").textContent).toBe("agent crashed");
      expect(screen.getByText(/Verifying\.\.\./)).toBeTruthy();
      expect(reviewApi.get).toHaveBeenCalledTimes(1);
      hub.emit(ev("review.status", { status: "ready" }));
      await waitFor(() => expect(reviewApi.get).toHaveBeenCalledTimes(2));
      expect(screen.queryByTestId("review-error")).toBeNull();
    });

    it("takes an approval and a widened diff", async () => {
      const { hub } = await renderWithSession();
      hub.emit(ev("review.approved", { commit: "abcdef1234567" }));
      expect(screen.getByTestId("review-approved-chip").textContent).toContain("abcdef1");
      hub.emit(ev("review.diff", { raw_diff: "diff --git a b" }));
      expect(screen.getByTestId("diff-stub").dataset.rawDiff).toBe("diff --git a b");
    });
  });

  describe("loading a PR", () => {
    it("ignores a load answer without a session", async () => {
      reviewApi.load.mockResolvedValue({ present: false });
      const { user } = renderPanel();
      await user.click(await screen.findByTestId("review-pr-row-7"));
      await waitFor(() => expect(screen.getByText("Select a PR to review")).toBeTruthy());
      expect(screen.getByTestId("review-pr-row-7")).toHaveProperty("disabled", false);
    });

    it("keeps a session that settled before the load answered", async () => {
      const load = deferred<ReviewSessionResponse>();
      reviewApi.load.mockReturnValue(load.promise);
      const { user, hub } = renderPanel();
      await user.click(await screen.findByTestId("review-pr-row-7"));
      reviewApi.get.mockResolvedValue(present(session({ comments: [comment("settled")] })));
      hub.emit({ type: "review.status", channel_id: CH, data: { status: "ready" } } as WSEvent);
      expect(await screen.findByTestId("comment-settled")).toBeTruthy();
      await act(async () => load.resolve(present(session({ status: "loading", loading_pr: 7, comments: [] }))));
      expect(screen.getByTestId("comment-settled")).toBeTruthy();
    });

    it("shows a non-Error load failure as text", async () => {
      reviewApi.load.mockRejectedValue("load gone");
      const { user } = renderPanel();
      await user.click(await screen.findByTestId("review-pr-row-7"));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "load gone");
    });
  });

  describe("running, more paths", () => {
    it("refuses to start on a session without a PR number", async () => {
      const { user } = await renderWithSession(session({ pr: undefined }));
      await user.click(screen.getByTestId("review-run-btn"));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "No review session loaded — pick a PR first, then run the loop.");
      expect(reviewApi.start).not.toHaveBeenCalled();
    });

    it("takes the started session and shows its empty-reviewing note", async () => {
      reviewApi.start.mockResolvedValue({ run_id: "run-1", session: session({ comments: [] }) });
      const { user } = await renderWithSession();
      await user.click(screen.getByTestId("review-run-btn"));
      expect(await screen.findByText("Reviewing... comments will appear inline as the agent emits them.")).toBeTruthy();
      expect(screen.queryByTestId("comment-c1")).toBeNull();
    });

    it("shows a non-Error start failure as text", async () => {
      reviewApi.start.mockRejectedValue("start gone");
      const { user } = await renderWithSession();
      await user.click(screen.getByTestId("review-run-btn"));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "start gone");
    });

    it("starts a one-shot review from the mode menu", async () => {
      window.localStorage.setItem("loop.review.lastMode", "review-fix");
      reviewApi.start.mockResolvedValue({ run_id: "run-1" });
      reviewApi.get.mockResolvedValue(present(session()));
      const { user } = renderPanel();
      await screen.findByTestId("diff-stub");
      await user.click(screen.getByRole("button", { name: "Choose review run mode" }));
      await user.click(screen.getByRole("menuitem", { name: "Run review (one-shot)" }));
      expect(reviewApi.start).toHaveBeenCalledWith(CH, expect.objectContaining({ mode: "review", model: "", effort: "" }));
    });

    it.each(["Run review (one-shot)", "Run review + fix loop"])("does nothing from %j once a run started meanwhile", async (item) => {
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Choose review run mode" }));
      act(() => window.dispatchEvent(new CustomEvent("loop.review.activeRunId.changed", { detail: { channelId: CH, runId: "run-9" } })));
      await user.click(screen.getByRole("menuitem", { name: item }));
      expect(reviewApi.start).not.toHaveBeenCalled();
    });
  });

  describe("run settings", () => {
    async function openSettings(s: ReviewSession = session()) {
      const r = await renderWithSession(s);
      await r.user.click(screen.getByRole("button", { name: "Run settings" }));
      return r;
    }
    const select = (name: string) => screen.getByRole("combobox", { name }) as HTMLSelectElement;

    it("ignores an iteration count that isn't a number", async () => {
      reviewApi.start.mockResolvedValue({ run_id: "run-1" });
      const r = await openSettings();
      const iters = screen.getByRole("spinbutton", { name: "Maximum review iterations (1-10)" }) as HTMLInputElement;
      await r.user.clear(iters);
      await r.user.click(screen.getByTestId("review-run-btn"));
      expect(reviewApi.start).toHaveBeenCalledWith(CH, expect.objectContaining({ max_iterations: 3 }));
    });

    it("commits the model and the effort, taking the daemon's session", async () => {
      reviewApi.setAgent.mockResolvedValueOnce(present(session({ model: "claude-opus" }))).mockResolvedValueOnce({ present: false });
      vi.mocked(fetchAgentConfig).mockResolvedValue({ default_model: "", default_effort: "", models: ["claude-opus"] } as never);
      const { user } = await openSettings();
      await waitFor(() => expect(within(select("Model the review run uses")).getByRole("option", { name: "opus" })).toBeTruthy());
      await user.selectOptions(select("Model the review run uses"), "claude-opus");
      expect(reviewApi.setAgent).toHaveBeenCalledWith(CH, "claude-opus", "");
      await waitFor(() => expect(select("Model the review run uses").value).toBe("claude-opus"));
      await user.selectOptions(select("Reasoning effort the review run uses"), "xhigh");
      expect(reviewApi.setAgent).toHaveBeenLastCalledWith(CH, "claude-opus", "xhigh");
      expect(within(select("Reasoning effort the review run uses")).getByRole("option", { name: "xhigh (recommended)" })).toBeTruthy();
    });

    it.each([new Error("agent rejected"), "agent gone"])("shows a failed model change (%s)", async (err) => {
      reviewApi.setAgent.mockRejectedValue(err);
      const { user } = await openSettings();
      await user.selectOptions(select("Reasoning effort the review run uses"), "max");
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", String(err).replace(/^Error: /, ""));
    });

    it("commits the scope and shows a failed one", async () => {
      reviewApi.setScope
        .mockResolvedValueOnce(present(session({ scope: "full" })))
        .mockResolvedValueOnce({ present: false })
        .mockRejectedValueOnce(new Error("scope rejected"))
        .mockRejectedValueOnce("scope gone");
      const { user } = await openSettings();
      await user.selectOptions(select("What the review run reviews"), "full");
      expect(reviewApi.setScope).toHaveBeenCalledWith(CH, "full");
      await waitFor(() => expect(select("What the review run reviews").value).toBe("full"));
      await user.selectOptions(select("What the review run reviews"), "incremental");
      await user.selectOptions(select("What the review run reviews"), "");
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "scope rejected");
      await user.selectOptions(select("What the review run reviews"), "incremental");
      await waitFor(() => expect(screen.getByTestId("review-error").textContent).toBe("scope gone"));
    });

    it("commits a fork mode straight away, and a custom one once it has an id", async () => {
      reviewApi.setFork.mockImplementation(async (_ch, mode, sid) => present(session({ fork_mode: mode, fork_session_id: sid })));
      const { user } = await openSettings();
      await user.selectOptions(select("Session the review run forks from"), "current");
      expect(reviewApi.setFork).toHaveBeenCalledWith(CH, "current", "");
      await user.selectOptions(select("Session the review run forks from"), "custom");
      expect(reviewApi.setFork).toHaveBeenCalledTimes(1);
      const id = screen.getByRole("textbox", { name: "Claude session id to fork" });
      // No id yet: the input is outlined as missing one.
      expect(id.getAttribute("style")).toContain("border: 1px solid");
      await user.type(id, "  sess-1 {Enter}");
      expect(reviewApi.setFork).toHaveBeenLastCalledWith(CH, "custom", "sess-1");
      await waitFor(() => expect(id.getAttribute("style")).not.toBe(""));
      // The same id again isn't a change.
      await user.click(id);
      await user.tab();
      expect(reviewApi.setFork).toHaveBeenCalledTimes(2);
      // Back to custom with an id typed commits it at once.
      await user.selectOptions(select("Session the review run forks from"), "");
      await user.selectOptions(select("Session the review run forks from"), "custom");
      expect(reviewApi.setFork).toHaveBeenLastCalledWith(CH, "custom", "sess-1");
    });

    it("leaves an empty custom id uncommitted on blur, and a plain key typed", async () => {
      const { user } = await openSettings();
      await user.selectOptions(select("Session the review run forks from"), "custom");
      const id = screen.getByRole("textbox", { name: "Claude session id to fork" });
      await user.click(id);
      await user.keyboard("   ");
      await user.tab();
      expect(reviewApi.setFork).not.toHaveBeenCalled();
    });

    it("reverts the typed id when the daemon refuses the fork", async () => {
      reviewApi.setFork.mockResolvedValueOnce({ present: false }).mockResolvedValueOnce({ present: false }).mockRejectedValueOnce(new Error("no such session")).mockRejectedValueOnce("fork gone");
      const { user } = await openSettings(session({ fork_mode: "custom", fork_session_id: "sess-0" }));
      const id = screen.getByRole("textbox", { name: "Claude session id to fork" }) as HTMLInputElement;
      expect(id.value).toBe("sess-0");
      await user.selectOptions(select("Session the review run forks from"), "current");
      await user.selectOptions(select("Session the review run forks from"), "custom");
      await user.clear(screen.getByRole("textbox", { name: "Claude session id to fork" }));
      await user.type(screen.getByRole("textbox", { name: "Claude session id to fork" }), "bad{Enter}");
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "no such session");
      expect((screen.getByRole("textbox", { name: "Claude session id to fork" }) as HTMLInputElement).value).toBe("sess-0");
      await user.selectOptions(select("Session the review run forks from"), "");
      await waitFor(() => expect(screen.getByTestId("review-error").textContent).toBe("fork gone"));
    });
  });

  describe("stats", () => {
    it("toggles the stats view from the picker", async () => {
      const { user } = renderPanel();
      await screen.findByTestId("review-pr-list");
      await user.click(screen.getByTestId("review-stats-btn"));
      expect(screen.getByText("Review stats")).toBeTruthy();
      expect(screen.getByTestId("stats-stub").textContent).toBe("undefined");
      expect(screen.queryByTestId("review-pr-list")).toBeNull();
      expect(screen.getByTestId("review-stats-btn").getAttribute("aria-pressed")).toBe("true");
      await user.click(screen.getByTestId("review-stats-btn"));
      expect(screen.queryByTestId("stats-stub")).toBeNull();
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });

    it("shows the session PR's stats from the ⋯ menu and goes back", async () => {
      const { user } = await renderWithSession();
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Stats" }));
      expect(screen.getByTestId("stats-stub").textContent).toBe("7");
      expect(screen.queryByTestId("diff-stub")).toBeNull();
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Back to the review" }));
      expect(screen.getByTestId("diff-stub")).toBeTruthy();
    });
  });

  describe("inline gate approval", () => {
    const gate: GateApprovalRequestedData = { req_id: "g-1", kind: "network", target: "example.com", source: "agent" };

    it("shows a pending gate when no chat panel is mounted and clears it once resolved", async () => {
      const onClear = vi.fn();
      const { user } = renderPanel({ hasChatPanel: false, gateApprovals: { agent: gate }, onClearGateApproval: onClear });
      const card = await screen.findByTestId("review-inline-approval");
      expect(within(card).getByTestId("approval-stub").textContent).toContain("g-1");
      await user.click(within(card).getByRole("button", { name: "Resolve gate" }));
      expect(onClear).toHaveBeenCalledWith("agent");
    });

    it("leaves the gate to the chat panel, and shows nothing without one pending", async () => {
      const { rerender } = render(<ReviewPanel channelId={CH} hasChatPanel gateApprovals={{ agent: gate }} />);
      await screen.findByTestId("review-pr-list");
      expect(screen.queryByTestId("review-inline-approval")).toBeNull();
      rerender(<ReviewPanel channelId={CH} hasChatPanel={false} />);
      expect(screen.queryByTestId("review-inline-approval")).toBeNull();
      rerender(<ReviewPanel channelId={CH} hasChatPanel={false} gateApprovals={{ agent: gate }} />);
      expect(screen.getByTestId("review-inline-approval")).toBeTruthy();
    });
  });

  describe("header", () => {
    it("shows a PR without a link, title, head or diff", async () => {
      await renderWithSession(session({ pr: { number: 12, base_ref: "main", head_ref: "feat/c", state: "open" } as ReviewPR, head_sha: undefined, raw_diff: undefined }));
      expect(screen.getByText("#12")).toBeTruthy();
      expect(screen.queryByRole("link")).toBeNull();
      expect(screen.getByTestId("diff-stub").dataset.rawDiff).toBe("");
      expect(screen.getByTestId("review-approve-btn").getAttribute("title")).toBe("The head commit isn't reviewed yet; Approve anyway is in the ⋯ menu");
    });

    it("links a PR without a title", async () => {
      await renderWithSession(session({ pr: { ...prs[0]!, title: undefined } as ReviewPR }));
      const link = screen.getByRole("link");
      expect(link.getAttribute("href")).toBe("https://example.com/pr/7");
      expect(link.textContent).toBe("#7");
    });

    it("names the head in an approvable session's Approve tooltip and a running review's wait", async () => {
      const s = session({ reviewed_sha: "abcdef1234567", comments: [] });
      const { hub } = await renderWithSession(s);
      expect(screen.getByTestId("review-approve-btn").getAttribute("title")).toBe("Approve the PR on GitHub at abcdef1");
      reviewApi.get.mockResolvedValue(present({ ...s, status: "reviewing" }));
      hub.emit({ type: "review.status", channel_id: CH, data: { status: "reviewing" } } as WSEvent);
      expect(screen.getByTestId("review-approve-btn").getAttribute("title")).toBe("Wait for the review to finish");
    });
  });

  describe("pushing, more paths", () => {
    it("clears a landed comment's outline after a moment", async () => {
      vi.useFakeTimers();
      reviewApi.pushOne.mockResolvedValue();
      reviewApi.get.mockResolvedValue(present(session()));
      render(<ReviewPanel channelId={CH} />);
      await act(async () => {});
      act(() => screen.getByRole("button", { name: "Push c1" }).click());
      await act(async () => {});
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("landed");
      await act(async () => vi.advanceTimersByTimeAsync(2500));
      expect(screen.getByTestId("comment-c1").dataset.pushState).toBe("");
    });

    it("keeps no session when a push lands after the review closed", async () => {
      const push = deferred<void>();
      reviewApi.pushOne.mockReturnValue(push.promise);
      reviewApi.deleteSession.mockResolvedValue();
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Push c1" }));
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Close review" }));
      await screen.findByTestId("review-pr-list");
      await act(async () => push.resolve());
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });

    it("keeps no session when a delete lands after the review closed", async () => {
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const del = deferred<void>();
      reviewApi.deleteComment.mockReturnValue(del.promise);
      reviewApi.deleteSession.mockResolvedValue();
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Delete c1" }));
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Close review" }));
      await screen.findByTestId("review-pr-list");
      await act(async () => del.resolve());
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });

    it.each([
      ["push", "Push c1", () => reviewApi.pushOne.mockRejectedValue("push gone")],
      ["delete", "Delete c1", () => reviewApi.deleteComment.mockRejectedValue("push gone")],
      ["send", "Address c1", () => sendMock.mockRejectedValue("push gone")],
    ])("shows a non-Error %s failure as text", async (_name, button, arrange) => {
      vi.spyOn(window, "confirm").mockReturnValue(true);
      arrange();
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: button }));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "push gone");
    });

    it("hides the Push all notice after a while, unless a newer one replaced it", async () => {
      vi.useFakeTimers();
      reviewApi.pushAll.mockResolvedValueOnce({ pushed: 2, skipped: 0, failed: 0 }).mockResolvedValueOnce({ pushed: 1, skipped: 0, failed: 0 });
      reviewApi.get.mockResolvedValue(present(session()));
      render(<ReviewPanel channelId={CH} />);
      await act(async () => {});
      const pushAll = async () => {
        act(() => screen.getByRole("button", { name: "More review actions" }).click());
        act(() =>
          within(screen.getByRole("menu"))
            .getByRole("menuitem", { name: /^Push all to GitHub/ })
            .click(),
        );
        await act(async () => {});
      };
      await pushAll();
      expect(screen.getByTestId("review-push-notice").textContent).toBe("✓ Pushed 2 comments to GitHub");
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      await pushAll();
      expect(screen.getByTestId("review-push-notice").textContent).toBe("✓ Pushed 1 comment to GitHub");
      // The first notice's timer leaves the newer one up.
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(screen.getByTestId("review-push-notice").textContent).toBe("✓ Pushed 1 comment to GitHub");
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(screen.queryByTestId("review-push-notice")).toBeNull();
    });

    it("reports failed pushes without their errors, and a non-Error failure", async () => {
      reviewApi.pushAll.mockResolvedValueOnce({ pushed: 0, skipped: 0, failed: 2 }).mockRejectedValueOnce("push all gone");
      reviewApi.get.mockResolvedValue(present(session()));
      const { user } = renderPanel();
      await screen.findByTestId("diff-stub");
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: /^Push all to GitHub/ }));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "2 comment(s) failed to push: ");
      expect(screen.queryByTestId("review-push-notice")).toBeNull();
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: /^Push all to GitHub/ }));
      await waitFor(() => expect(screen.getByTestId("review-error").textContent).toBe("push all gone"));
    });
  });

  describe("All actions", () => {
    const mixed = () =>
      session({
        comments: [comment("c1", { verdict: "real", impact: "blocking" }), comment("c2", { verdict: "real", impact: "nice_to_have" }), comment("g1", { source: "github", github_id: 5 })],
      });

    it("Check all asks the agent to verify the session's findings", async () => {
      const s = mixed();
      const { user } = await renderWithSession(s);
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Check all (2)" }));
      expect(sendMock).toHaveBeenCalledWith(CH, buildCheckAllPrompt(s));
    });

    it("Address blocking only sends the blocking findings", async () => {
      const s = mixed();
      const { user } = await renderWithSession(s);
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Address blocking only (1)" }));
      expect(sendMock).toHaveBeenCalledWith(CH, buildAddressAllPrompt([s.comments[0]!], s));
    });

    it.each([
      ["Check all (2)", new Error("check failed"), "check failed"],
      ["Check all (2)", "check gone", "check gone"],
      ["Address all (3)", new Error("address failed"), "address failed"],
      ["Address all (3)", "address gone", "address gone"],
    ])("shows a failed %s", async (item, err, text) => {
      sendMock.mockRejectedValue(err);
      const { user } = await renderWithSession(mixed());
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: item }));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", text);
    });

    it.each([
      ["sync", "Sync with GitHub", () => reviewApi.sync.mockRejectedValue("sync gone")],
      ["close", "Close review", () => reviewApi.deleteSession.mockRejectedValue("sync gone")],
    ])("shows a non-Error %s failure as text", async (_name, item, arrange) => {
      arrange();
      const { user } = await renderWithSession();
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: item }));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "sync gone");
    });

    it("keeps the session when a sync answers without one", async () => {
      reviewApi.sync.mockResolvedValue({ present: false });
      const { user } = await renderWithSession();
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: "Sync with GitHub" }));
      await waitFor(() => expect(reviewApi.sync).toHaveBeenCalled());
      expect(screen.getByTestId("diff-stub")).toBeTruthy();
    });
  });

  describe("stopping and resyncing, more paths", () => {
    it("cancels only the loop between reviews, and shows a non-Error failure", async () => {
      vi.mocked(cancelWorkflowRun)
        .mockResolvedValueOnce(undefined as never)
        .mockRejectedValueOnce("cancel gone");
      const { user } = await renderWithSession();
      act(() => window.dispatchEvent(new CustomEvent("loop.review.activeRunId.changed", { detail: { channelId: CH, runId: "run-3" } })));
      await user.click(screen.getByTestId("review-stop-btn"));
      expect(cancelWorkflowRun).toHaveBeenCalledWith("run-3");
      expect(reviewApi.stop).not.toHaveBeenCalled();
      await user.click(screen.getByTestId("review-stop-btn"));
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "cancel gone");
    });

    it("keeps the session when the refetch after Push all finds none", async () => {
      reviewApi.pushAll.mockResolvedValue({ pushed: 0, skipped: 1, failed: 0 });
      const { user } = await renderWithSession();
      reviewApi.get.mockResolvedValue({ present: false });
      await user.click(within(await openMore(user)).getByRole("menuitem", { name: /^Push all to GitHub/ }));
      await waitFor(() => expect(reviewApi.get).toHaveBeenCalledTimes(2));
      expect(screen.getByTestId("diff-stub")).toBeTruthy();
      expect(screen.queryByTestId("review-push-notice")).toBeNull();
    });

    it("clears the typed id when a refused fork had none stored", async () => {
      reviewApi.setFork.mockRejectedValue(new Error("refused"));
      const { user } = await renderWithSession();
      await user.click(screen.getByRole("button", { name: "Run settings" }));
      await user.selectOptions(screen.getByRole("combobox", { name: "Session the review run forks from" }), "current");
      expect(await screen.findByTestId("review-error")).toHaveProperty("textContent", "refused");
    });

    it("names the last run's scope in the header", async () => {
      await renderWithSession(session({ last_run_scope: "full" }));
      expect(screen.getByTestId("review-scope-chip").textContent).toBe(" · last run: whole PR");
    });

    it("lists a PR without a title", async () => {
      reviewApi.listPRs.mockResolvedValue([{ ...prs[0]!, title: undefined } as ReviewPR]);
      renderPanel();
      expect((await screen.findByTestId("review-pr-row-7")).textContent).toContain("#7");
    });
  });

  describe("approving, more paths", () => {
    const approvable = () => session({ reviewed_sha: "abcdef1234567", comments: [] });

    it("closes the dialog on Cancel", async () => {
      const { user } = await renderWithSession(approvable());
      await user.click(screen.getByTestId("review-approve-btn"));
      await user.click(within(screen.getByTestId("review-approve-dialog")).getByRole("button", { name: "Cancel" }));
      expect(screen.queryByTestId("review-approve-dialog")).toBeNull();
      expect(reviewApi.approve).not.toHaveBeenCalled();
    });

    it("keeps no session when an approval lands after the session went away", async () => {
      const approve = deferred<string>();
      reviewApi.approve.mockReturnValue(approve.promise);
      const { user, hub } = await renderWithSession(approvable());
      await user.click(screen.getByTestId("review-approve-btn"));
      await user.click(screen.getByTestId("review-approve-submit"));
      reviewApi.get.mockResolvedValue({ present: false });
      hub.emit({ type: "review.status", channel_id: CH, data: { status: "loading" } } as WSEvent);
      await screen.findByTestId("review-pr-list");
      await act(async () => approve.resolve("abcdef1234567"));
      expect(screen.queryByTestId("review-approve-dialog")).toBeNull();
      expect(screen.getByTestId("review-pr-list")).toBeTruthy();
    });
  });
});
