import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createWorktreeThread } from "../../api/channels";
import { deleteBranch, fetchBranches, switchBranch } from "../../api/git";
import { useEventStream } from "../../hooks/useEventStream";
import { darkColors } from "../../theme";
import { BranchesPanel } from "./BranchesPanel";

vi.mock("../../api/git", () => ({
  deleteBranch: vi.fn(),
  fetchBranches: vi.fn(),
  switchBranch: vi.fn(),
}));

vi.mock("../../api/channels", () => ({
  createWorktreeThread: vi.fn(),
}));

vi.mock("../../hooks/useEventStream", () => ({
  useEventStream: vi.fn(),
}));

const fetchMock = vi.mocked(fetchBranches);
const switchMock = vi.mocked(switchBranch);
const deleteMock = vi.mocked(deleteBranch);
const createWtMock = vi.mocked(createWorktreeThread);
const streamMock = vi.mocked(useEventStream);

// The panel's latest onEvent handed to the event stream.
const lastOnEvent = () => streamMock.mock.lastCall![0].onEvent;

const renderPanel = (props: Partial<React.ComponentProps<typeof BranchesPanel>> = {}) => render(<BranchesPanel channelId="ch1" isWorktree={false} hasBranch {...props} />);

const rowButtons = async (branch: string) => {
  const row = (await screen.findByText(branch)).closest("div")?.parentElement as HTMLElement;
  return row;
};

describe("BranchesPanel", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    fetchMock.mockResolvedValue({ branches: ["main", "feature"], current: "main", worktrees: [] });
  });

  it("tells a worktree its branches live in the parent channel", () => {
    renderPanel({ isWorktree: true });
    expect(screen.getByText("Branches are managed from the parent channel")).toBeTruthy();
  });

  it("says when there is no repository", () => {
    renderPanel({ hasBranch: false });
    expect(screen.getByText("No git repository detected")).toBeTruthy();
  });

  it("shows loading, then the branches with the current one marked", async () => {
    renderPanel();
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(await screen.findByText("main *")).toBeTruthy();
    expect(screen.getByText("feature")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith("ch1");
    // The current branch has only +wt; the other has Switch, +wt and Delete.
    expect(screen.getAllByTitle("Switch to this branch")).toHaveLength(1);
    expect(screen.getAllByTitle("Create worktree from this branch")).toHaveLength(2);
    expect(screen.getAllByTitle("Delete this branch")).toHaveLength(1);
  });

  it("shows no branches when the list is empty or missing", async () => {
    fetchMock.mockResolvedValue({} as never);
    renderPanel();
    expect(await screen.findByText("No branches")).toBeTruthy();
  });

  it("shows no branches when fetching fails", async () => {
    fetchMock.mockRejectedValue(new Error("boom"));
    renderPanel();
    expect(await screen.findByText("No branches")).toBeTruthy();
  });

  it("reloads on channel created and deleted events only", async () => {
    renderPanel();
    await screen.findByText("main *");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(streamMock).toHaveBeenCalledWith(expect.objectContaining({ channelId: "ch1" }));
    await act(async () => {
      lastOnEvent()({ type: "message.created" } as never);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      lastOnEvent()({ type: "channel.created" } as never);
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await act(async () => {
      lastOnEvent()({ type: "channel.deleted" } as never);
    });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("switches branch, reloads and reports the change", async () => {
    const onBranchChanged = vi.fn();
    let resolve: () => void = () => {};
    switchMock.mockReturnValue(new Promise<void>((r) => (resolve = r)));
    renderPanel({ onBranchChanged });
    const btn = await screen.findByTitle("Switch to this branch");
    await userEvent.click(btn);
    expect(switchMock).toHaveBeenCalledWith("ch1", "feature");
    expect(btn.textContent).toBe("...");
    expect((btn as HTMLButtonElement).disabled).toBe(true);
    fetchMock.mockResolvedValue({ branches: ["main", "feature"], current: "feature", worktrees: [] });
    await act(async () => resolve());
    expect(await screen.findByText("feature *")).toBeTruthy();
    expect(onBranchChanged).toHaveBeenCalledTimes(1);
  });

  it("switches without an onBranchChanged callback", async () => {
    switchMock.mockResolvedValue();
    renderPanel();
    await userEvent.click(await screen.findByTitle("Switch to this branch"));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("ignores a failed switch and re-enables the button", async () => {
    const onBranchChanged = vi.fn();
    switchMock.mockRejectedValue(new Error("dirty"));
    renderPanel({ onBranchChanged });
    const btn = await screen.findByTitle("Switch to this branch");
    await userEvent.click(btn);
    await waitFor(() => expect(btn.textContent).toBe("Switch"));
    expect(onBranchChanged).not.toHaveBeenCalled();
  });

  it("creates a worktree and selects its thread", async () => {
    const onSelectThread = vi.fn();
    let resolve: (v: { threadId: string; worktreePath: string }) => void = () => {};
    createWtMock.mockReturnValue(new Promise((r) => (resolve = r)));
    renderPanel({ onSelectThread });
    const mainWt = (await screen.findAllByTitle("Create worktree from this branch"))[0]!;
    await userEvent.click(mainWt);
    expect(createWtMock).toHaveBeenCalledWith("ch1", "main");
    expect(mainWt.textContent).toBe("...");
    await act(async () => resolve({ threadId: "t9", worktreePath: "/w" }));
    expect(onSelectThread).toHaveBeenCalledWith("t9");
    await waitFor(() => expect(mainWt.textContent).toBe("+wt"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not select a thread without an id or a callback", async () => {
    const onSelectThread = vi.fn();
    createWtMock.mockResolvedValue({ threadId: "", worktreePath: "" });
    const { unmount } = renderPanel({ onSelectThread });
    await userEvent.click((await screen.findAllByTitle("Create worktree from this branch"))[0]!);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(onSelectThread).not.toHaveBeenCalled();
    unmount();

    createWtMock.mockResolvedValue({ threadId: "t1", worktreePath: "" });
    renderPanel();
    await userEvent.click((await screen.findAllByTitle("Create worktree from this branch"))[0]!);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));
  });

  it("ignores a failed worktree creation", async () => {
    createWtMock.mockRejectedValue(new Error("exists"));
    renderPanel();
    const btn = (await screen.findAllByTitle("Create worktree from this branch"))[0]!;
    await userEvent.click(btn);
    await waitFor(() => expect(btn.textContent).toBe("+wt"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("asks before deleting and deletes on Yes", async () => {
    let resolve: () => void = () => {};
    deleteMock.mockReturnValue(new Promise<void>((r) => (resolve = r)));
    renderPanel();
    const del = await screen.findByTitle("Delete this branch");
    await userEvent.click(del);
    expect(screen.getByText("Delete?")).toBeTruthy();
    // A mousedown inside the popover keeps it open.
    fireEvent.mouseDown(screen.getByText("Delete?"));
    expect(screen.getByText("Delete?")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(screen.queryByText("Delete?")).toBeNull();
    expect(deleteMock).toHaveBeenCalledWith("ch1", "feature");
    expect(del.textContent).toBe("...");
    expect(del.style.color).toBe(hex(darkColors.textDim));
    fetchMock.mockResolvedValue({ branches: ["main"], current: "main", worktrees: [] });
    await act(async () => resolve());
    await waitFor(() => expect(screen.queryByText("feature")).toBeNull());
  });

  it("ignores a failed delete", async () => {
    deleteMock.mockRejectedValue(new Error("unmerged"));
    renderPanel();
    const del = await screen.findByTitle("Delete this branch");
    await userEvent.click(del);
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    await waitFor(() => expect(del.textContent).toBe("Delete"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("closes the confirmation on No or a click outside", async () => {
    renderPanel();
    const del = await screen.findByTitle("Delete this branch");
    await userEvent.click(del);
    await userEvent.click(screen.getByRole("button", { name: "No" }));
    expect(screen.queryByText("Delete?")).toBeNull();

    await userEvent.click(del);
    fireEvent.mouseDown(document.body);
    expect(screen.queryByText("Delete?")).toBeNull();
    expect(deleteMock).not.toHaveBeenCalled();
  });

  it("highlights buttons on hover and restores them on leave", async () => {
    renderPanel();
    await rowButtons("feature");
    const del = screen.getByTitle("Delete this branch");
    const sw = screen.getByTitle("Switch to this branch");
    const wt = screen.getAllByTitle("Create worktree from this branch")[0]!;
    for (const [btn, hoverColor, restColor] of [
      [sw, darkColors.white, darkColors.textLight],
      [wt, darkColors.white, darkColors.textLight],
      [del, darkColors.dangerText, "#ef4444"],
    ] as const) {
      fireEvent.mouseEnter(btn);
      expect(btn.style.color).toBe(hex(hoverColor));
      expect(btn.style.borderColor).toBe(hex(darkColors.textLight));
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).toBe(hex(restColor));
      expect(btn.style.borderColor).toBe(hex(darkColors.border));
    }

    await userEvent.click(del);
    const yes = screen.getByRole("button", { name: "Yes" });
    fireEvent.mouseEnter(yes);
    expect(yes.style.color).toBe(hex(darkColors.white));
    expect(yes.style.background).toBe(hex(darkColors.dangerHoverBg));
    fireEvent.mouseLeave(yes);
    expect(yes.style.color).toBe(hex(darkColors.dangerText));
    expect(yes.style.background).toBe(hex(darkColors.dangerBg));

    const no = screen.getByRole("button", { name: "No" });
    fireEvent.mouseEnter(no);
    expect(no.style.color).toBe(hex(darkColors.textLight));
    expect(no.style.borderColor).toBe(hex(darkColors.textDim));
    fireEvent.mouseLeave(no);
    expect(no.style.color).toBe(hex(darkColors.textDim));
    expect(no.style.borderColor).toBe(hex(darkColors.border));
  });
});

// jsdom reports inline colors as rgb(); normalize a theme color the same way.
function hex(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}
