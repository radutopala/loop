import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BranchInfo, PRInfo } from "../../api/git";
import { fetchBranches, switchBranch } from "../../api/loopApi";
import { useOpenPRs } from "../shared/OpenPRs";
import { HeaderBranchPicker } from "./HeaderBranchPicker";

vi.mock("../../api/loopApi", () => ({ fetchBranches: vi.fn(), switchBranch: vi.fn() }));
vi.mock("../shared/OpenPRs", async (importOriginal) => ({ ...(await importOriginal<typeof import("../shared/OpenPRs")>()), useOpenPRs: vi.fn() }));

const branchesMock = vi.mocked(fetchBranches);
const switchMock = vi.mocked(switchBranch);
const prsMock = vi.mocked(useOpenPRs);
const writeText = vi.fn();

const info = (over: Partial<BranchInfo> = {}): BranchInfo => ({ branches: ["main", "feature/x", "fix-Bug"], current: "main", worktrees: [], ...over });
const pr: PRInfo = { number: 12, url: "https://forge.example/pr/12", base_ref: "main", head_ref: "feature/x", state: "OPEN", title: "Add x" };

async function openPicker(props: Partial<Parameters<typeof HeaderBranchPicker>[0]> = {}) {
  const r = render(<HeaderBranchPicker channelId="ch-1" branch="main" {...props} />);
  fireEvent.click(screen.getByTitle("Branch"));
  const picker = screen.getByTestId("branch-picker");
  await waitFor(() => expect(within(picker).queryByText("No branches found")).toBeNull());
  return { ...r, picker };
}

beforeEach(() => {
  vi.clearAllMocks();
  branchesMock.mockResolvedValue(info());
  switchMock.mockResolvedValue(undefined as never);
  prsMock.mockReturnValue({ prs: [], error: null });
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
});

afterEach(() => {
  delete (navigator as { clipboard?: unknown }).clipboard;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("HeaderBranchPicker", () => {
  it("shows the branch and dims under the mouse", () => {
    render(<HeaderBranchPicker channelId="ch-1" branch="main" />);
    const btn = screen.getByTitle("Branch");
    expect(btn.textContent).toBe("main");
    fireEvent.mouseEnter(btn);
    expect(btn.style.opacity).toBe("0.8");
    fireEvent.mouseLeave(btn);
    expect(btn.style.opacity).toBe("1");
    expect(screen.queryByTestId("branch-picker")).toBeNull();
  });

  it("lists the channel's branches on open, marks the current one and focuses the search", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { picker } = await openPicker();
    expect(branchesMock).toHaveBeenCalledWith("ch-1");
    expect(prsMock).toHaveBeenLastCalledWith("ch-1", false);
    const search = within(picker).getByPlaceholderText("Search branches");
    act(() => vi.runOnlyPendingTimers());
    expect(document.activeElement).toBe(search);
    expect(within(picker).getByText("main").parentElement!.textContent).toBe("main✓");
    expect(within(picker).getByText("feature/x").parentElement!.textContent).toBe("feature/x");
    expect(within(picker).queryByText("+wt")).toBeNull();
    expect(picker.style.maxHeight).toBe("min(400px, 70vh)");
  });

  it("filters branches by the search, ignoring case, and says when none match", async () => {
    const { picker } = await openPicker();
    const search = within(picker).getByPlaceholderText("Search branches");
    fireEvent.change(search, { target: { value: "BUG" } });
    expect(within(picker).getByText("fix-Bug")).toBeTruthy();
    expect(within(picker).queryByText("main")).toBeNull();
    fireEvent.change(search, { target: { value: "nope" } });
    expect(within(picker).getByText("No branches found")).toBeTruthy();
  });

  it("switches to a picked branch and reports the change", async () => {
    const onBranchChanged = vi.fn();
    const { picker } = await openPicker({ onBranchChanged });
    fireEvent.click(within(picker).getByText("feature/x"));
    expect(screen.queryByTestId("branch-picker")).toBeNull();
    await waitFor(() => expect(onBranchChanged).toHaveBeenCalledOnce());
    expect(switchMock).toHaveBeenCalledWith("ch-1", "feature/x");
  });

  it("switches without a change callback", async () => {
    const { picker } = await openPicker();
    fireEvent.click(within(picker).getByText("feature/x"));
    await waitFor(() => expect(switchMock).toHaveBeenCalledWith("ch-1", "feature/x"));
  });

  it("just closes when the current branch is picked", async () => {
    const { picker } = await openPicker({ onBranchChanged: vi.fn() });
    fireEvent.click(within(picker).getByText("main"));
    expect(screen.queryByTestId("branch-picker")).toBeNull();
    expect(switchMock).not.toHaveBeenCalled();
  });

  it.each([
    { name: "an Error's message", err: new Error("dirty tree"), msg: "dirty tree" },
    { name: "a generic message otherwise", err: "nope", msg: "Failed to switch branch" },
  ])("reports a failed switch with $name", async ({ err, msg }) => {
    switchMock.mockRejectedValue(err);
    const onError = vi.fn();
    const { picker } = await openPicker({ onError });
    fireEvent.click(within(picker).getByText("feature/x"));
    await waitFor(() => expect(onError).toHaveBeenCalledWith(msg));
  });

  it("swallows a failed switch without an error callback", async () => {
    switchMock.mockRejectedValue(new Error("x"));
    const { picker } = await openPicker();
    fireEvent.click(within(picker).getByText("feature/x"));
    await waitFor(() => expect(switchMock).toHaveBeenCalled());
  });

  it("marks the repo's current branch over the shown one", async () => {
    branchesMock.mockResolvedValue(info({ current: "feature/x" }));
    const { picker } = await openPicker();
    expect(within(picker).getByText("feature/x").parentElement!.textContent).toBe("feature/x✓");
    expect(within(picker).getByText("main").parentElement!.textContent).toBe("main");
  });

  it("marks the shown branch when the listing names no current one", async () => {
    branchesMock.mockResolvedValue(info({ current: undefined as unknown as string }));
    const { picker } = await openPicker();
    expect(within(picker).getByText("main").parentElement!.textContent).toBe("main✓");
  });

  it("highlights a row and its copy button under the mouse, and copies the name", async () => {
    const { picker } = await openPicker();
    const row = within(picker).getByText("feature/x").closest("button")!.parentElement!;
    fireEvent.mouseEnter(row);
    expect(row.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(row);
    expect(row.style.backgroundColor).toBe("transparent");
    const cp = within(row).getByTitle("Copy branch name");
    fireEvent.mouseEnter(cp);
    expect(cp.style.color).toBe("rgb(236, 236, 236)");
    fireEvent.mouseLeave(cp);
    expect(cp.style.color).not.toBe("rgb(236, 236, 236)");
    fireEvent.click(cp);
    expect(writeText).toHaveBeenCalledWith("feature/x");
    expect(screen.getByTestId("branch-picker")).toBeTruthy();
  });

  it("starts a worktree thread from a branch", async () => {
    const onCreateWorktree = vi.fn().mockResolvedValue(undefined);
    const { picker } = await openPicker({ onCreateWorktree });
    const wt = within(picker).getByTitle("New worktree thread from feature/x");
    fireEvent.mouseEnter(wt);
    const lit = wt.style.color;
    fireEvent.mouseLeave(wt);
    expect(wt.style.color).not.toBe(lit);
    fireEvent.click(wt);
    expect(onCreateWorktree).toHaveBeenCalledWith("ch-1", "feature/x");
    expect(screen.queryByTestId("branch-picker")).toBeNull();
  });

  it("closes on a click outside, not inside", async () => {
    const { picker } = await openPicker();
    fireEvent.mouseDown(picker);
    expect(screen.getByTestId("branch-picker")).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId("branch-picker")).toBeNull();
  });

  it("stops listening for outside clicks once closed or unmounted", async () => {
    const remove = vi.spyOn(document, "removeEventListener");
    const { unmount } = await openPicker();
    unmount();
    expect(remove).toHaveBeenCalledWith("mousedown", expect.any(Function));
  });

  it("logs a failed branch listing", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    branchesMock.mockRejectedValue(new Error("down"));
    render(<HeaderBranchPicker channelId="ch-1" branch="main" />);
    fireEvent.click(screen.getByTitle("Branch"));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching branches:", expect.any(Error)));
    expect(screen.getByText("No branches found")).toBeTruthy();
  });

  it("lists the repo's open PRs and opens a picked one", async () => {
    prsMock.mockReturnValue({ prs: [pr], error: null });
    const onOpenPR = vi.fn();
    const { picker } = await openPicker({ onOpenPR });
    expect(prsMock).toHaveBeenLastCalledWith("ch-1", true);
    expect(within(picker).getByPlaceholderText("Search branches, PRs & worktrees")).toBeTruthy();
    expect(picker.style.height).toBe("min(400px, 70vh)");
    fireEvent.click(screen.getByTestId("branch-picker-prs-12"));
    expect(onOpenPR).toHaveBeenCalledWith("ch-1", pr, undefined);
    expect(screen.queryByTestId("branch-picker")).toBeNull();
  });

  it("hides the PR section with none to show", async () => {
    prsMock.mockReturnValue({ prs: [], error: null });
    await openPicker({ onOpenPR: vi.fn() });
    expect(screen.queryByTestId("branch-picker-prs")).toBeNull();
    expect(screen.getByPlaceholderText("Search branches")).toBeTruthy();
  });

  it("lists worktrees to go to or import", async () => {
    branchesMock.mockResolvedValue(
      info({
        worktrees: [
          { path: "/r/wt-a", branch: "a", thread_id: "th-a" },
          { path: "/r/wt-b", branch: "b" },
        ],
      }),
    );
    const onSelectThread = vi.fn();
    const onImportWorktree = vi.fn().mockResolvedValue(undefined);
    const { picker } = await openPicker({ onSelectThread, onImportWorktree });
    await waitFor(() => expect(within(picker).getByTestId("branch-picker-worktrees")).toBeTruthy());
    expect(within(picker).getByPlaceholderText("Search branches & worktrees")).toBeTruthy();
    fireEvent.click(within(picker).getByTestId("worktree-import"));
    expect(onImportWorktree).toHaveBeenCalledWith("ch-1", "/r/wt-b");
    expect(screen.queryByTestId("branch-picker")).toBeNull();

    fireEvent.click(screen.getByTitle("Branch"));
    await waitFor(() => expect(screen.getByTestId("worktree-go")).toBeTruthy());
    fireEvent.click(screen.getByTestId("worktree-go"));
    expect(onSelectThread).toHaveBeenCalledWith("th-a");
  });

  it("lists worktrees to go to without an import callback", async () => {
    branchesMock.mockResolvedValue(info({ worktrees: [{ path: "/r/wt-a", branch: "a", thread_id: "th-a" }] }));
    await openPicker({ onSelectThread: vi.fn() });
    await waitFor(() => expect(screen.getByTestId("worktree-go")).toBeTruthy());
    expect(screen.queryByTestId("worktree-import")).toBeNull();
  });

  it("hides worktrees with nothing to do with them", async () => {
    branchesMock.mockResolvedValue(info({ worktrees: [{ path: "/r/wt-a", branch: "a" }] }));
    await openPicker();
    expect(screen.queryByTestId("branch-picker-worktrees")).toBeNull();
  });
});
