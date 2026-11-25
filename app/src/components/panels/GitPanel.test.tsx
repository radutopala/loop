import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchRoots, type RootEntry } from "../../api/files";
import { type BranchInfo, type CommitEntry, type DiffResponse, fetchBranches, fetchCommitDiff, fetchCommits, fetchDiff, fetchPR, type PRInfo } from "../../api/loopApi";
import { getPRStatus, type PRStatus } from "../../api/pr";
import { useEventStream } from "../../hooks/useEventStream";
import type { WSEvent } from "../../types";
import type { FileLinkOpenDetail } from "../chat/FileLink";
import { GitPanel } from "./GitPanel";

vi.mock("../../api/files", () => ({ fetchRoots: vi.fn() }));
vi.mock("../../api/loopApi", () => ({
  fetchBranches: vi.fn(),
  fetchCommitDiff: vi.fn(),
  fetchCommits: vi.fn(),
  fetchDiff: vi.fn(),
  fetchPR: vi.fn(),
}));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn() }));
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

// DiffViewer lays out every hunk; the stub shows the props the panel hands it
// and wires its buttons to the panel's callbacks.
vi.mock("./DiffViewer", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./DiffViewer")>();
  return {
    ...actual,
    DiffViewer: (p: {
      files: { path: string; status?: "staged" | "unstaged" | "untracked" | "conflict" }[];
      parsedFiles: unknown[];
      expandedFiles: Set<string>;
      loading: boolean;
      hasData: boolean;
      totalFiles: number;
      rootIndex: number;
      onToggleFile: (key: string) => void;
      onExpandAll: () => void;
      onCollapseAll: () => void;
      onFileContextMenu: (e: React.MouseEvent, path: string) => void;
    }) => (
      <div
        data-testid="diff-stub"
        data-loading={String(p.loading)}
        data-has-data={String(p.hasData)}
        data-total={p.totalFiles}
        data-parsed={p.parsedFiles.length}
        data-root={p.rootIndex}
        data-expanded={[...p.expandedFiles].sort().join(",")}
      >
        <button onClick={() => p.onToggleFile(actual.fileKey(p.files[0] ?? { path: "" }))}>toggle first</button>
        <button onClick={p.onExpandAll}>expand all</button>
        <button onClick={p.onCollapseAll}>collapse all</button>
        <button onContextMenu={(e) => p.onFileContextMenu(e, "src/a.go")}>menu a.go</button>
      </div>
    ),
  };
});

vi.mock("./BranchesPanel", () => ({
  BranchesPanel: (p: { channelId: string; isWorktree: boolean; hasBranch: boolean; onBranchChanged?: () => void }) => (
    <div data-testid="branches-stub" data-channel={p.channelId} data-worktree={String(p.isWorktree)} data-has-branch={String(p.hasBranch)}>
      <button onClick={p.onBranchChanged}>branch changed</button>
    </div>
  ),
}));

vi.mock("./WorktreesPanel", () => ({
  WorktreesPanel: (p: { channelId: string; isWorktree: boolean; hasBranch: boolean }) => (
    <div data-testid="worktrees-stub" data-channel={p.channelId} data-worktree={String(p.isWorktree)} data-has-branch={String(p.hasBranch)} />
  ),
}));

vi.mock("./PRPanel", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./PRPanel")>()),
  PRPanel: (p: { channelId: string; status: PRStatus; onChanged: () => void }) => (
    <div data-testid="pr-stub" data-channel={p.channelId} data-number={p.status.number}>
      <button onClick={p.onChanged}>pr changed</button>
    </div>
  ),
}));

const api = {
  roots: vi.mocked(fetchRoots),
  branches: vi.mocked(fetchBranches),
  commitDiff: vi.mocked(fetchCommitDiff),
  commits: vi.mocked(fetchCommits),
  diff: vi.mocked(fetchDiff),
  pr: vi.mocked(fetchPR),
  prStatus: vi.mocked(getPRStatus),
};
const eventStream = vi.mocked(useEventStream);

const uncommitted: DiffResponse = {
  files: [{ path: "a.go", status: "unstaged", additions: 2, deletions: 1, binary: false }],
  diff: "d1",
  unstaged_diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n-x\n+y\n+z\n",
  total_additions: 2,
  total_deletions: 1,
};

const branchInfo: BranchInfo = { branches: ["feature", "main"], current: "feature", worktrees: [] };

const prInfo: PRInfo = { number: 7, url: "https://forge.example/pr/7", base_ref: "develop", head_ref: "feature", state: "OPEN", title: "Add login" };

const prStatus: PRStatus = {
  channel_id: "ch-1",
  kind: "pr",
  number: 7,
  head_sha: "abc",
  checks: { total: 2, passed: 1, failed: 1, pending: 0 },
  merge_state: "clean",
  auto_merge: false,
  pr_attention: "",
  auto_fix: false,
  auto_fix_default: false,
  attempts: 0,
  max_attempts: 3,
};

const roots: RootEntry[] = [
  { index: 0, path: "/repo", name: "repo" },
  { index: 1, path: "/extra", name: "extra" },
];

function commit(i: number): CommitEntry {
  return { hash: `h${i}`, short: `s${i}`, subject: `subject ${i}`, author: "Ann", date: "2026-01-02T10:00:00Z" };
}

function page(from: number, n: number): CommitEntry[] {
  return Array.from({ length: n }, (_, i) => commit(from + i));
}

function renderPanel(props: Partial<Parameters<typeof GitPanel>[0]> = {}) {
  const onClose = vi.fn();
  const user = userEvent.setup();
  const utils = render(<GitPanel channelId="ch-1" onClose={onClose} {...props} />);
  return { ...utils, onClose, user };
}

function emit(event: WSEvent) {
  const onEvent = eventStream.mock.lastCall?.[0].onEvent;
  act(() => onEvent?.(event));
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const stub = () => screen.getByTestId("diff-stub");
const firstRow = () => screen.getAllByTestId("commit-row")[0] as HTMLElement;

describe("GitPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    api.roots.mockResolvedValue([]);
    api.pr.mockResolvedValue({ present: false });
    api.prStatus.mockRejectedValue(new Error("no PR watching"));
    api.diff.mockResolvedValue(uncommitted);
    api.branches.mockResolvedValue(branchInfo);
    api.commits.mockResolvedValue([]);
    api.commitDiff.mockReturnValue(new Promise(() => {}));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("width", () => {
    it("defaults to 60% of the window", () => {
      renderPanel();
      expect(screen.getByTestId("git-panel").style.width).toBe(`${Math.floor(window.innerWidth * 0.6)}px`);
    });

    it("uses a stored width at or above the minimum and ignores a smaller one", () => {
      localStorage.setItem("loop-diff-panel-width", "400");
      const { unmount } = renderPanel();
      expect(screen.getByTestId("git-panel").style.width).toBe("400px");
      unmount();

      localStorage.setItem("loop-diff-panel-width", "100");
      renderPanel();
      expect(screen.getByTestId("git-panel").style.width).toBe(`${Math.floor(window.innerWidth * 0.6)}px`);
    });

    it("resizes from the left edge, clamps to the minimum and stores the result", () => {
      localStorage.setItem("loop-diff-panel-width", "400");
      renderPanel();
      const panel = screen.getByTestId("git-panel");
      const handle = panel.firstElementChild as HTMLDivElement;

      fireEvent.mouseEnter(handle);
      expect(handle.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseDown(handle, { clientX: 500 });
      expect(panel.style.userSelect).toBe("none");
      // A leave mid-drag keeps the handle lit.
      fireEvent.mouseLeave(handle);
      expect(handle.style.backgroundColor).not.toBe("transparent");

      fireEvent.mouseMove(document, { clientX: 450 });
      expect(panel.style.width).toBe("450px");
      fireEvent.mouseMove(document, { clientX: 900 });
      expect(panel.style.width).toBe("280px");
      fireEvent.mouseUp(document);
      expect(localStorage.getItem("loop-diff-panel-width")).toBe("280");
      expect(panel.style.userSelect).toBe("");

      // The listeners are gone after mouseup.
      fireEvent.mouseMove(document, { clientX: 100 });
      expect(panel.style.width).toBe("280px");
      fireEvent.mouseLeave(handle);
      expect(handle.style.backgroundColor).toBe("transparent");
    });

    it("has no resize handle when maximized", () => {
      renderPanel({ maximized: true });
      const panel = screen.getByTestId("git-panel");
      expect(panel.style.width).toBe("100%");
      expect((panel.firstElementChild as HTMLElement).style.cursor).not.toBe("col-resize");
    });
  });

  describe("uncommitted diff", () => {
    it("loads the diff, shows its stats and toggles files", async () => {
      const { user } = renderPanel();
      expect(api.diff).toHaveBeenCalledWith("ch-1", undefined, undefined, 0);
      await waitFor(() => expect(stub().dataset.loading).toBe("false"));
      expect(stub().dataset.hasData).toBe("true");
      expect(stub().dataset.total).toBe("1");
      expect(stub().dataset.parsed).toBe("1");
      expect(screen.getByText("+2")).toBeTruthy();
      expect(screen.getByText("-1")).toBeTruthy();

      await user.click(screen.getByText("expand all"));
      expect(stub().dataset.expanded).toBe("unstaged:a.go");
      await user.click(screen.getByText("toggle first"));
      expect(stub().dataset.expanded).toBe("");
      await user.click(screen.getByText("toggle first"));
      expect(stub().dataset.expanded).toBe("unstaged:a.go");
      await user.click(screen.getByText("collapse all"));
      expect(stub().dataset.expanded).toBe("");
    });

    it("expands nothing before the diff arrives", async () => {
      api.diff.mockReturnValue(new Promise(() => {}));
      const { user } = renderPanel();
      await user.click(screen.getByText("expand all"));
      expect(stub().dataset.expanded).toBe("");
      expect(stub().dataset.hasData).toBe("false");
    });

    it("polls, keeping the viewer while the diff is unchanged and remounting it when it changes", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      renderPanel();
      await waitFor(() => expect(stub().dataset.hasData).toBe("true"));
      const first = stub();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(api.diff).toHaveBeenCalledTimes(2);
      expect(stub()).toBe(first);

      api.diff.mockResolvedValue({ ...uncommitted, diff: "d2" });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(api.diff).toHaveBeenCalledTimes(3);
      expect(stub()).not.toBe(first);
    });

    it("refreshes the diff and the PR from the refresh button", async () => {
      const { user } = renderPanel();
      await waitFor(() => expect(api.pr).toHaveBeenCalledTimes(1));
      const refresh = screen.getByTitle("Refresh");
      fireEvent.mouseEnter(refresh);
      expect(refresh.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseLeave(refresh);
      expect(refresh.style.backgroundColor).toBe("transparent");

      await user.click(refresh);
      expect(api.diff).toHaveBeenCalledTimes(2);
      expect(api.pr).toHaveBeenLastCalledWith("ch-1", true);
    });
  });

  describe("file context menu", () => {
    it("opens the file and copies its relative and home-relative absolute paths", async () => {
      const { user } = renderPanel({ dirPath: "/home/u/repo", homeDir: "/home/u" });
      const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
      const opened = vi.fn();
      window.addEventListener("loop:open-file", opened);

      fireEvent.contextMenu(screen.getByText("menu a.go"), { clientX: 10, clientY: 20 });
      await user.click(screen.getByRole("menuitem", { name: "Open file" }));
      const detail = (opened.mock.lastCall?.[0] as CustomEvent<FileLinkOpenDetail> | undefined)?.detail;
      expect(detail).toEqual({ channelId: "ch-1", target: { rootIndex: 0, relPath: "src/a.go" }, line: null });
      window.removeEventListener("loop:open-file", opened);
      expect(screen.queryByRole("menu")).toBeNull();

      fireEvent.contextMenu(screen.getByText("menu a.go"));
      await user.click(screen.getByRole("menuitem", { name: "Copy relative path" }));
      expect(write).toHaveBeenLastCalledWith("src/a.go");

      fireEvent.contextMenu(screen.getByText("menu a.go"));
      await user.click(screen.getByRole("menuitem", { name: "Copy absolute path" }));
      expect(write).toHaveBeenLastCalledWith("~/repo/src/a.go");
    });

    it("offers only the copy items without a channel and roots the path at / without a dir", async () => {
      const { user } = renderPanel({ channelId: null });
      const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
      fireEvent.contextMenu(screen.getByText("menu a.go"));
      expect(screen.queryByRole("menuitem", { name: "Open file" })).toBeNull();
      await user.click(screen.getByRole("menuitem", { name: "Copy absolute path" }));
      expect(write).toHaveBeenCalledWith("/src/a.go");
    });
  });

  describe("without a channel", () => {
    it("loads nothing and shows nothing for the channel-bound tabs", async () => {
      const { user } = renderPanel({ channelId: null, hasBranch: true });
      expect(api.pr).not.toHaveBeenCalled();
      expect(api.roots).not.toHaveBeenCalled();
      expect(api.diff).not.toHaveBeenCalled();
      await user.click(screen.getByRole("button", { name: "Branches" }));
      expect(screen.queryByTestId("branches-stub")).toBeNull();
      expect(api.branches).not.toHaveBeenCalled();
      await user.click(screen.getByRole("button", { name: "Worktrees" }));
      expect(screen.queryByTestId("worktrees-stub")).toBeNull();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      expect(api.commits).not.toHaveBeenCalled();
    });

    it("clears the PR when the channel goes away", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      api.prStatus.mockResolvedValue(prStatus);
      const { rerender, onClose } = renderPanel({ embedded: true });
      await screen.findByTestId("git-panel-pr-tab");
      rerender(<GitPanel channelId={null} embedded onClose={onClose} />);
      await waitFor(() => expect(screen.queryByTestId("git-panel-pr-tab")).toBeNull());
      expect(screen.queryByTitle(/#7/)).toBeNull();
    });
  });

  describe("branches diff", () => {
    it("diffs the current branch into main and refetches on a pick", async () => {
      api.branches.mockResolvedValue({
        branches: ["main", "feature"],
        current: "feature",
        worktrees: [
          { path: "/wt", branch: "wt-b" },
          { path: "/bare", branch: "" },
        ],
      });
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      expect(api.branches).toHaveBeenCalledWith("ch-1", 0);
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", "main", "feature", 0));
      const from = screen.getByTitle("Changes from") as HTMLSelectElement;
      const into = screen.getByTitle("Land into") as HTMLSelectElement;
      expect(from.value).toBe("feature");
      expect(into.value).toBe("main");
      expect([...into.options].map((o) => o.value)).toEqual(["feature", "main", "wt-b"]);
      // The branch diff shows its stats like the uncommitted one.
      expect(screen.getByText("+2")).toBeTruthy();

      await user.selectOptions(from, "wt-b");
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", "main", "wt-b", 0));
      await user.selectOptions(into, "feature");
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", "feature", "wt-b", 0));
    });

    it("keeps a pick the new branch list still has", async () => {
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      const into = (await screen.findByTitle("Land into")) as HTMLSelectElement;
      await waitFor(() => expect(into.value).toBe("main"));
      await user.selectOptions(into, "feature");
      await user.click(screen.getByRole("button", { name: "Uncommitted Diff" }));
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await waitFor(() => expect(api.branches).toHaveBeenCalledTimes(2));
      expect((screen.getByTitle("Land into") as HTMLSelectElement).value).toBe("feature");
    });

    it("falls back to the current branch, then the first, then nothing", async () => {
      api.branches.mockResolvedValueOnce({ branches: ["b", "a"], current: "b", worktrees: [] });
      const first = renderPanel();
      await first.user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await waitFor(() => expect((screen.getByTitle("Land into") as HTMLSelectElement).value).toBe("b"));
      first.unmount();

      api.branches.mockResolvedValueOnce({ branches: ["b", "a"] } as unknown as BranchInfo);
      const second = renderPanel();
      await second.user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await waitFor(() => expect((screen.getByTitle("Land into") as HTMLSelectElement).value).toBe("a"));
      // No current branch: the from-side asks for one and no branch diff loads.
      expect((screen.getByTitle("Changes from") as HTMLSelectElement).value).toBe("");
      expect(screen.getByRole("option", { name: "from…" })).toBeTruthy();
      second.unmount();

      api.diff.mockClear();
      api.branches.mockResolvedValueOnce({ branches: [] } as unknown as BranchInfo);
      const third = renderPanel();
      await third.user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await waitFor(() => expect(api.branches).toHaveBeenCalledTimes(3));
      expect(screen.getByRole("option", { name: "into…" })).toBeTruthy();
      expect(api.diff).toHaveBeenCalledTimes(1);
      expect(api.diff).toHaveBeenCalledWith("ch-1", undefined, undefined, 0);
    });

    it("logs a failed branch fetch", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      api.branches.mockRejectedValue(new Error("boom"));
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching branch info:", expect.any(Error)));
    });
  });

  describe("PR", () => {
    it("seeds the land-into branch from the PR's base once per channel", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      const into = screen.getByTitle("Land into") as HTMLSelectElement;
      await waitFor(() => expect(into.value).toBe("develop"));
      expect([...into.options].map((o) => o.value)).toContain("develop");

      await user.selectOptions(into, "main");
      await user.click(screen.getByTitle("Refresh"));
      await waitFor(() => expect(api.pr).toHaveBeenCalledTimes(2));
      expect(into.value).toBe("main");
    });

    it("leaves the PR's base out of another root's branches", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      api.roots.mockResolvedValue(roots);
      const { user } = renderPanel({ embedded: true });
      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      await user.click(await screen.findByTestId("git-panel-root-select"));
      await user.click(screen.getByRole("menuitemradio", { name: /extra/ }));
      await waitFor(() => expect(api.branches).toHaveBeenCalledWith("ch-1", 1));
      const into = screen.getByTitle("Land into") as HTMLSelectElement;
      await waitFor(() => expect([...into.options].map((o) => o.value)).toEqual(["feature", "main"]));
      expect(into.value).toBe("main");
    });

    it("shows the PR tab with its badge and the PR panel, which refreshes the status", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      api.prStatus.mockResolvedValue(prStatus);
      const { user } = renderPanel({ embedded: true });
      const tab = await screen.findByTestId("git-panel-pr-tab");
      expect(tab.textContent).toBe("PR✗ 1");
      expect(api.prStatus).toHaveBeenCalledWith("ch-1");

      await user.click(tab);
      expect(screen.getByTestId("pr-stub").dataset.number).toBe("7");
      // The diff stats belong to the diff tabs.
      expect(screen.queryByText("+2")).toBeNull();
      await user.click(screen.getByText("pr changed"));
      expect(api.prStatus).toHaveBeenCalledTimes(2);

      // Refresh on the PR tab refetches only the PR.
      const diffCalls = api.diff.mock.calls.length;
      await user.click(screen.getByTitle("Refresh"));
      expect(api.pr).toHaveBeenLastCalledWith("ch-1", true);
      expect(api.diff).toHaveBeenCalledTimes(diffCalls);

      // The PR going away leaves the PR tab with a note.
      api.pr.mockResolvedValue({ present: false });
      await user.click(screen.getByTitle("Refresh"));
      expect(await screen.findByText("No status for this branch's PR/MR.")).toBeTruthy();
    });

    it("labels a merge request MR and leaves out an empty badge", async () => {
      api.pr.mockResolvedValue({ present: true, pr: { ...prInfo, kind: "mr" } });
      api.prStatus.mockResolvedValue({ ...prStatus, kind: "mr", checks: { total: 0, passed: 0, failed: 0, pending: 0 } });
      renderPanel({ embedded: true });
      const tab = await screen.findByTestId("git-panel-pr-tab");
      expect(tab.textContent).toBe("MR");
      expect(screen.getByTitle("!7 Add login (open)").textContent).toBe("MR !7");
    });

    it("hides the PR tab when the status fails or the PR lookup fails", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      const { unmount } = renderPanel({ embedded: true });
      await screen.findByTitle("#7 Add login (open)");
      await waitFor(() => expect(api.prStatus).toHaveBeenCalled());
      expect(screen.queryByTestId("git-panel-pr-tab")).toBeNull();
      unmount();

      api.pr.mockRejectedValue(new Error("down"));
      renderPanel({ embedded: true });
      await waitFor(() => expect(api.pr).toHaveBeenCalledTimes(2));
      expect(screen.queryByTitle("#7 Add login (open)")).toBeNull();
    });

    it("opens the PR in the browser from its chip", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      const open = vi.spyOn(window, "open").mockReturnValue(null);
      const { user } = renderPanel({ embedded: true });
      await user.click(await screen.findByTitle("#7 Add login (open)"));
      expect(open).toHaveBeenCalledWith("https://forge.example/pr/7", "_blank", "noopener,noreferrer");
    });
  });

  describe("roots", () => {
    it("scopes the diff to the picked root from the toolbar picker", async () => {
      api.roots.mockResolvedValue(roots);
      const { user } = renderPanel({ embedded: true, hasBranch: true });
      await user.click(await screen.findByTestId("git-panel-root-select"));
      await user.click(screen.getByRole("menuitemradio", { name: /extra/ }));
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", undefined, undefined, 1));
      await waitFor(() => expect(stub().dataset.root).toBe("1"));

      // Tabs without a root scope hide the toolbar picker.
      await user.click(screen.getByRole("button", { name: "Worktrees" }));
      expect(screen.queryByTestId("git-panel-root-select")).toBeNull();
    });

    it("portals the picker into the pane header with a leaf id", async () => {
      api.roots.mockResolvedValue(roots);
      const slot = document.createElement("div");
      slot.id = "pane-header-slot-L1";
      document.body.appendChild(slot);
      renderPanel({ embedded: true, leafId: "L1" });
      expect(await within(slot).findByTestId("git-panel-root-select")).toBeTruthy();
      slot.remove();
    });

    it("hides the picker for a single root or a failed root fetch", async () => {
      api.roots.mockRejectedValue(new Error("down"));
      renderPanel({ embedded: true });
      await waitFor(() => expect(api.roots).toHaveBeenCalled());
      expect(screen.queryByTestId("git-panel-root-select")).toBeNull();
    });

    it("drops a root answer for a channel already left", async () => {
      const ok = deferred<RootEntry[]>();
      const bad = deferred<RootEntry[]>();
      api.roots.mockReturnValueOnce(ok.promise).mockReturnValueOnce(bad.promise).mockResolvedValueOnce([]);
      const { rerender, onClose } = renderPanel({ embedded: true });
      rerender(<GitPanel channelId="ch-2" embedded onClose={onClose} />);
      rerender(<GitPanel channelId="ch-3" embedded onClose={onClose} />);
      await act(async () => {
        ok.resolve(roots);
        bad.reject(new Error("late"));
        await Promise.resolve();
      });
      expect(screen.queryByTestId("git-panel-root-select")).toBeNull();
    });
  });

  describe("commits", () => {
    it("lists the current branch's commits and pages in more on scroll", async () => {
      api.commits.mockResolvedValueOnce(page(0, 50)).mockResolvedValueOnce(page(0, 50)).mockResolvedValueOnce(page(50, 3));
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      await waitFor(() => expect(screen.getAllByTestId("commit-row")).toHaveLength(50));
      expect(api.commits).toHaveBeenCalledWith("ch-1", "feature", 50, 0, 0);
      // The commit list has no diff stats.
      expect(screen.queryByText("+2")).toBeNull();

      const list = firstRow().parentElement as HTMLDivElement;
      Object.defineProperty(list, "scrollHeight", { value: 1000, configurable: true });
      Object.defineProperty(list, "clientHeight", { value: 950, configurable: true });
      fireEvent.scroll(list);
      await waitFor(() => expect(screen.getAllByTestId("commit-row")).toHaveLength(53));
      expect(api.commits).toHaveBeenLastCalledWith("ch-1", "feature", 50, 50, 0);

      // A short page ends the list.
      const calls = api.commits.mock.calls.length;
      fireEvent.scroll(list);
      expect(api.commits).toHaveBeenCalledTimes(calls);
    });

    it("stops paging when a page fails, and empties the list when the first one fails", async () => {
      api.commits.mockResolvedValueOnce(page(0, 50)).mockResolvedValueOnce(page(0, 50)).mockRejectedValueOnce(new Error("down"));
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      await waitFor(() => expect(screen.getAllByTestId("commit-row")).toHaveLength(50));
      const list = firstRow().parentElement as HTMLDivElement;
      Object.defineProperty(list, "scrollHeight", { value: 1000, configurable: true });
      Object.defineProperty(list, "clientHeight", { value: 950, configurable: true });
      fireEvent.scroll(list);
      await waitFor(() => expect(api.commits).toHaveBeenCalledTimes(3));
      await waitFor(() => expect(screen.queryByText("Loading more...")).toBeNull());
      fireEvent.scroll(list);
      expect(api.commits).toHaveBeenCalledTimes(3);
      expect(screen.getAllByTestId("commit-row")).toHaveLength(50);

      api.commits.mockRejectedValue(new Error("down"));
      await user.selectOptions(screen.getByTitle("Branch"), "main");
      expect(await screen.findByText("No commits")).toBeTruthy();
    });

    it("waits for a branch before listing commits", async () => {
      api.branches.mockResolvedValue({ branches: [], current: "", worktrees: [] });
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      await waitFor(() => expect(api.branches).toHaveBeenCalled());
      expect(api.commits).not.toHaveBeenCalled();
      expect(screen.getByText("No commits")).toBeTruthy();
    });

    it("opens a commit's diff over the list and closes it on back, a branch switch or the tab", async () => {
      api.commits.mockResolvedValue([{ ...commit(1), subject: "Add login" }]);
      const { user } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      await user.click(await screen.findByText("Add login"));
      expect(screen.getByTestId("commit-diff")).toBeTruthy();
      expect(api.commitDiff).toHaveBeenCalledWith("ch-1", "h1", 0);
      expect((firstRow().parentElement as HTMLElement).parentElement?.style.display).toBe("none");

      await user.click(screen.getByTestId("commit-diff-back"));
      expect(screen.queryByTestId("commit-diff")).toBeNull();

      await user.click(screen.getByText("Add login"));
      // The tab comes before the diff's own back button.
      await user.click(screen.getAllByRole("button", { name: "Commits" })[0] as HTMLElement);
      expect(screen.queryByTestId("commit-diff")).toBeNull();

      await user.click(screen.getByText("Add login"));
      await user.selectOptions(screen.getByTitle("Branch"), "main");
      expect(screen.queryByTestId("commit-diff")).toBeNull();
    });

    it("closes an open commit when the channel goes away", async () => {
      api.commits.mockResolvedValue([commit(1)]);
      const { user, rerender, onClose } = renderPanel();
      await user.click(screen.getByRole("button", { name: "Commits" }));
      await user.click(await screen.findByText("subject 1"));
      rerender(<GitPanel channelId={null} onClose={onClose} />);
      expect(screen.queryByTestId("commit-diff")).toBeNull();
    });
  });

  describe("events", () => {
    it("takes this channel's PR status from events and ignores other channels'", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      renderPanel({ embedded: true });
      await screen.findByTitle("#7 Add login (open)");
      expect(eventStream).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: "ch-1" }));

      emit({ type: "channel.pr", channel_id: "ch-2", data: { ...prStatus, channel_id: "ch-2" } } as WSEvent);
      expect(screen.queryByTestId("git-panel-pr-tab")).toBeNull();
      emit({ type: "channel.pr", channel_id: "ch-1", data: prStatus } as WSEvent);
      expect(screen.getByTestId("git-panel-pr-tab")).toBeTruthy();
      // PR events don't reload the diff.
      expect(api.pr).toHaveBeenCalledTimes(1);
    });

    it("reloads once after a burst of events and refetches the PR on run end and channel updates", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      renderPanel();
      await waitFor(() => expect(api.diff).toHaveBeenCalledTimes(1));

      emit({ type: "agent.status", channel_id: "ch-1", data: { status: "running" } } as WSEvent);
      expect(api.pr).toHaveBeenCalledTimes(1);
      emit({ type: "agent.status", channel_id: "ch-1", data: { status: "idle" } } as WSEvent);
      expect(api.pr).toHaveBeenLastCalledWith("ch-1", true);
      emit({ type: "channel.updated", channel_id: "ch-1" } as WSEvent);
      expect(api.pr).toHaveBeenLastCalledWith("ch-1", false);
      emit({ type: "message.created", channel_id: "ch-1" } as WSEvent);
      expect(api.pr).toHaveBeenCalledTimes(3);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(api.diff).toHaveBeenCalledTimes(2);
    });

    it("cancels a pending reload on unmount", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const { unmount } = renderPanel();
      await waitFor(() => expect(api.diff).toHaveBeenCalledTimes(1));
      emit({ type: "message.created", channel_id: "ch-1" } as WSEvent);
      unmount();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000);
      });
      expect(api.diff).toHaveBeenCalledTimes(1);
    });

    it("unmounts cleanly without a pending reload", () => {
      const clear = vi.spyOn(globalThis, "clearTimeout");
      const { unmount } = renderPanel();
      unmount();
      expect(clear).not.toHaveBeenCalled();
    });
  });

  // Each layout draws its own copy of the tab strip and the branch pickers.
  describe.each([
    { layout: "embedded", props: { embedded: true } },
    { layout: "docked", props: {} },
    { layout: "maximized", props: { maximized: true } },
  ])("$layout tabs", ({ layout, props }) => {
    it("switches between every tab", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      api.prStatus.mockResolvedValue(prStatus);
      api.diff.mockResolvedValue({ ...uncommitted, total_additions: 0, total_deletions: 3 });
      api.commits.mockResolvedValue([commit(1)]);
      const onStatusChange = vi.fn();
      const { user } = renderPanel({ ...props, hasBranch: true, isWorktree: true, onStatusChange });
      await screen.findByTestId("git-panel-pr-tab");
      expect(await screen.findByText("-3")).toBeTruthy();
      expect(screen.getByText("+0")).toBeTruthy();

      await user.click(screen.getByRole("button", { name: "Branches" }));
      expect(screen.getByTestId("branches-stub").dataset.worktree).toBe("true");
      expect(screen.queryByText("-3")).toBeNull();
      await user.click(screen.getByText("branch changed"));
      expect(onStatusChange).toHaveBeenCalled();
      expect(screen.queryByTitle("Refresh") !== null).toBe(layout !== "embedded");

      await user.click(screen.getByRole("button", { name: "Worktrees" }));
      expect(screen.getByTestId("worktrees-stub").dataset.hasBranch).toBe("true");
      expect(screen.queryByTitle("Refresh")).toBeNull();

      await user.click(screen.getByTestId("git-panel-pr-tab"));
      expect(screen.getByTestId("pr-stub")).toBeTruthy();

      await user.click(screen.getByRole("button", { name: "Branches Diff" }));
      const from = (await screen.findByTitle("Changes from")) as HTMLSelectElement;
      await waitFor(() => expect(from.value).toBe("feature"));
      await user.selectOptions(from, "main");
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", "develop", "main", 0));
      await user.selectOptions(screen.getByTitle("Land into"), "feature");
      await waitFor(() => expect(api.diff).toHaveBeenCalledWith("ch-1", "feature", "main", 0));

      await user.click(screen.getByRole("button", { name: "Commits" }));
      await screen.findByText("subject 1");
      await user.selectOptions(screen.getByTitle("Branch"), "main");
      await waitFor(() => expect(api.commits).toHaveBeenCalledWith("ch-1", "main", 50, 0, 0));

      await user.click(screen.getByRole("button", { name: "Uncommitted Diff" }));
      expect(stub()).toBeTruthy();
    });
  });

  describe("tab content after props change", () => {
    it("falls back to defaults when hasBranch goes away on its tab", async () => {
      const { user, rerender, onClose } = renderPanel({ embedded: true, hasBranch: true });
      await user.click(screen.getByRole("button", { name: "Branches" }));
      rerender(<GitPanel channelId="ch-1" embedded onClose={onClose} />);
      expect(screen.getByTestId("branches-stub").dataset.hasBranch).toBe("false");
      expect(screen.getByTestId("branches-stub").dataset.worktree).toBe("false");

      rerender(<GitPanel channelId="ch-1" embedded hasBranch onClose={onClose} />);
      await user.click(screen.getByRole("button", { name: "Worktrees" }));
      rerender(<GitPanel channelId="ch-1" embedded onClose={onClose} />);
      expect(screen.getByTestId("worktrees-stub").dataset.hasBranch).toBe("false");
    });
  });

  describe("standalone header", () => {
    it("toggles maximize and closes", async () => {
      const onToggleMaximize = vi.fn();
      const { user, onClose } = renderPanel({ onToggleMaximize });
      await user.click(screen.getByTitle("Maximize panel"));
      expect(onToggleMaximize).toHaveBeenCalledTimes(1);
      await user.click(screen.getByTitle("Close panel"));
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(screen.queryByTitle("Toggle sidebar")).toBeNull();
    });

    it("shows the window chrome when maximized with the sidebar closed", async () => {
      api.pr.mockResolvedValue({ present: true, pr: prInfo });
      const onToggleSidebar = vi.fn();
      const onOpenPalette = vi.fn();
      const onToggleMaximize = vi.fn();
      const { user } = renderPanel({
        maximized: true,
        sidebarOpen: false,
        dirPath: "/repo",
        branch: "feature",
        tabBar: <span>tab bar</span>,
        onToggleSidebar,
        onOpenPalette,
        onToggleMaximize,
      });
      expect(screen.getByText("tab bar")).toBeTruthy();
      expect(screen.getByText("/repo")).toBeTruthy();
      expect(screen.getByText("feature")).toBeTruthy();
      expect(await screen.findByTitle("#7 Add login (open)")).toBeTruthy();
      expect(screen.getByText("Ctrl+K")).toBeTruthy();
      const sidebar = screen.getByTitle("Toggle sidebar");
      expect((sidebar.parentElement as HTMLElement).style.paddingLeft).toBe("76px");

      await user.click(sidebar);
      expect(onToggleSidebar).toHaveBeenCalled();
      await user.click(screen.getByTitle("Search messages (Cmd+K)"));
      expect(onOpenPalette).toHaveBeenCalled();
      await user.click(screen.getByTitle("Restore panel"));
      expect(onToggleMaximize).toHaveBeenCalled();
    });

    it("shows the Mac shortcut and pads less with the sidebar open, without a branch", () => {
      vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
      renderPanel({ maximized: true, sidebarOpen: true, dirPath: "/repo", onToggleSidebar: vi.fn(), onOpenPalette: vi.fn() });
      expect(screen.getByText("⌘K")).toBeTruthy();
      expect((screen.getByTitle("Toggle sidebar").parentElement as HTMLElement).style.paddingLeft).toBe("4px");
      expect(screen.queryByText("|")).toBeNull();
    });

    it("leaves out the window chrome when maximized without its callbacks or dir", () => {
      renderPanel({ maximized: true });
      expect(screen.queryByTitle("Toggle sidebar")).toBeNull();
      expect(screen.queryByTitle("Search messages (Cmd+K)")).toBeNull();
      expect(screen.queryByTitle("Restore panel")).toBeNull();
    });
  });
});
