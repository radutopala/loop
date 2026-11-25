import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { removeWorktree, setChannelLocked, setWorktreeLocked } from "../../api/channels";
import { fetchBranches, type WorktreeInfo } from "../../api/git";
import { useEventStream } from "../../hooks/useEventStream";
import { darkColors } from "../../theme";
import { WorktreesPanel } from "./WorktreesPanel";

vi.mock("../../api/git", () => ({
  fetchBranches: vi.fn(),
}));

vi.mock("../../api/channels", () => ({
  removeWorktree: vi.fn(),
  setChannelLocked: vi.fn(),
  setWorktreeLocked: vi.fn(),
}));

vi.mock("../../hooks/useEventStream", () => ({
  useEventStream: vi.fn(),
}));

const fetchMock = vi.mocked(fetchBranches);
const removeMock = vi.mocked(removeWorktree);
const lockChannelMock = vi.mocked(setChannelLocked);
const lockWorktreeMock = vi.mocked(setWorktreeLocked);
const streamMock = vi.mocked(useEventStream);

const lastOnEvent = () => streamMock.mock.lastCall![0].onEvent;

const imported: WorktreeInfo = { path: "/repo/.wt/wt-a", branch: "feat-a", thread_id: "t1" };
const loose: WorktreeInfo = { path: "/repo/.wt/wt-b", branch: "feat-b" };

const setWorktrees = (worktrees?: WorktreeInfo[]) => fetchMock.mockResolvedValue({ branches: [], current: "", worktrees } as never);

const renderPanel = (props: Partial<React.ComponentProps<typeof WorktreesPanel>> = {}) => render(<WorktreesPanel channelId="ch1" isWorktree={false} hasBranch {...props} />);

// The row holding a worktree's branch label.
const row = async (branch: string) => (await screen.findByText(branch)).closest("[style*='relative']") as HTMLElement;

function rgb(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}

describe("WorktreesPanel", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    setWorktrees([imported, loose]);
  });

  it("tells a worktree its worktrees live in the parent channel", () => {
    renderPanel({ isWorktree: true });
    expect(screen.getByText("Worktrees are managed from the parent channel")).toBeTruthy();
  });

  it("says when there is no repository", () => {
    renderPanel({ hasBranch: false });
    expect(screen.getByText("No git repository detected")).toBeTruthy();
  });

  it("shows loading, then each worktree with the actions its kind allows", async () => {
    renderPanel();
    expect(screen.getByText("Loading...")).toBeTruthy();
    const a = await row("feat-a");
    expect(within(a).getByText("feat-a")).toBeTruthy();
    expect(within(a).getByText("Go")).toBeTruthy();
    expect(within(a).getByTitle("Lock worktree")).toBeTruthy();
    expect(within(a).getByTitle("Remove worktree from disk and delete thread")).toBeTruthy();
    const b = await row("feat-b");
    expect(within(b).getByText("Import")).toBeTruthy();
    expect(within(b).getByTitle("Remove worktree from disk")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith("ch1");
  });

  it("labels a detached worktree and falls back to the full path for a trailing slash", async () => {
    setWorktrees([{ path: "/repo/x/", branch: "" }]);
    renderPanel();
    expect(await screen.findByText("detached")).toBeTruthy();
    expect(screen.getByText("/repo/x/")).toBeTruthy();
  });

  it("marks locked worktrees and hides their Delete", async () => {
    setWorktrees([
      { ...imported, locked: true },
      { ...loose, locked: true },
    ]);
    renderPanel();
    await row("feat-a");
    expect(screen.getAllByLabelText("locked")).toHaveLength(2);
    expect(screen.getAllByTitle("Unlock worktree")).toHaveLength(2);
    expect(screen.queryByText("Delete")).toBeNull();
  });

  it("shows no worktrees when there are none or fetching fails", async () => {
    setWorktrees(undefined);
    const { unmount } = renderPanel();
    expect(await screen.findByText("No worktrees")).toBeTruthy();
    unmount();
    fetchMock.mockRejectedValue(new Error("boom"));
    renderPanel();
    expect(await screen.findByText("No worktrees")).toBeTruthy();
  });

  it("reloads on channel created and deleted events only", async () => {
    renderPanel();
    await row("feat-a");
    expect(streamMock).toHaveBeenCalledWith(expect.objectContaining({ channelId: "ch1" }));
    await act(async () => lastOnEvent()({ type: "other" } as never));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => lastOnEvent()({ type: "channel.created" } as never));
    await act(async () => lastOnEvent()({ type: "channel.deleted" } as never));
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("opens an imported worktree's thread, and does nothing without a handler", async () => {
    const onSelectThread = vi.fn();
    const { unmount } = renderPanel({ onSelectThread });
    await userEvent.click(within(await row("feat-a")).getByText("Go"));
    expect(onSelectThread).toHaveBeenCalledWith("t1");
    unmount();
    renderPanel();
    await userEvent.click(within(await row("feat-a")).getByText("Go"));
  });

  it("locks an imported worktree through its channel and a loose one by path", async () => {
    let resolve: () => void = () => {};
    lockChannelMock.mockReturnValue(new Promise<void>((r) => (resolve = r)));
    lockWorktreeMock.mockResolvedValue();
    renderPanel();
    const lockA = within(await row("feat-a")).getByTitle("Lock worktree");
    await userEvent.click(lockA);
    expect(lockChannelMock).toHaveBeenCalledWith("t1", true);
    expect(lockA.textContent).toBe("...");
    await act(async () => resolve());
    await waitFor(() => expect(lockA.textContent).toBe("Lock"));
    expect(fetchMock).toHaveBeenCalledTimes(2);

    setWorktrees([imported, { ...loose, locked: true }]);
    await userEvent.click(within(await row("feat-b")).getByTitle("Lock worktree"));
    expect(lockWorktreeMock).toHaveBeenCalledWith("ch1", "/repo/.wt/wt-b", true);
    await waitFor(() => expect(within(screen.getByText("feat-b").closest("[style*='relative']") as HTMLElement).getByTitle("Unlock worktree")).toBeTruthy());
  });

  it("unlocks, and ignores a failed lock toggle", async () => {
    setWorktrees([{ ...imported, locked: true }]);
    lockChannelMock.mockRejectedValue(new Error("nope"));
    renderPanel();
    const btn = within(await row("feat-a")).getByTitle("Unlock worktree");
    await userEvent.click(btn);
    expect(lockChannelMock).toHaveBeenCalledWith("t1", false);
    await waitFor(() => expect(btn.textContent).toBe("Unlock"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("imports a loose worktree and reloads", async () => {
    let resolve: () => void = () => {};
    const onImportWorktree = vi.fn(() => new Promise<void>((r) => (resolve = r)));
    renderPanel({ onImportWorktree });
    const btn = within(await row("feat-b")).getByText("Import");
    await userEvent.click(btn);
    expect(onImportWorktree).toHaveBeenCalledWith("ch1", "/repo/.wt/wt-b");
    expect(btn.textContent).toBe("...");
    await act(async () => resolve());
    await waitFor(() => expect(btn.textContent).toBe("Import"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("ignores Import without a handler and a failed import", async () => {
    const { unmount } = renderPanel();
    await userEvent.click(within(await row("feat-b")).getByText("Import"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    unmount();

    const onImportWorktree = vi.fn().mockRejectedValue(new Error("x"));
    renderPanel({ onImportWorktree });
    const btn = within(await row("feat-b")).getByText("Import");
    await userEvent.click(btn);
    await waitFor(() => expect(btn.textContent).toBe("Import"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("deletes an imported worktree with its thread after confirming", async () => {
    let resolve: () => void = () => {};
    removeMock.mockReturnValue(new Promise<void>((r) => (resolve = r)));
    renderPanel();
    const del = within(await row("feat-a")).getByTitle("Remove worktree from disk and delete thread");
    await userEvent.click(del);
    fireEvent.mouseDown(screen.getByText("Delete?"));
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(removeMock).toHaveBeenCalledWith("ch1", "/repo/.wt/wt-a", "t1");
    expect(del.textContent).toBe("...");
    expect(del.style.color).toBe(rgb(darkColors.textDim));
    expect(screen.queryByText("Delete?")).toBeNull();
    setWorktrees([loose]);
    await act(async () => resolve());
    await waitFor(() => expect(screen.queryByText("feat-a")).toBeNull());
  });

  it("removes a loose worktree by path after confirming", async () => {
    let resolve: () => void = () => {};
    removeMock.mockReturnValue(new Promise<void>((r) => (resolve = r)));
    renderPanel();
    const del = within(await row("feat-b")).getByTitle("Remove worktree from disk");
    await userEvent.click(del);
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(removeMock).toHaveBeenCalledWith("ch1", "/repo/.wt/wt-b");
    expect(del.textContent).toBe("...");
    expect(del.style.color).toBe(rgb(darkColors.textDim));
    await act(async () => resolve());
    await waitFor(() => expect(del.textContent).toBe("Delete"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("ignores failed deletes and removes", async () => {
    removeMock.mockRejectedValue(new Error("busy"));
    renderPanel();
    const delA = within(await row("feat-a")).getByTitle("Remove worktree from disk and delete thread");
    await userEvent.click(delA);
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    await waitFor(() => expect(delA.textContent).toBe("Delete"));
    const delB = within(await row("feat-b")).getByTitle("Remove worktree from disk");
    await userEvent.click(delB);
    await userEvent.click(screen.getByRole("button", { name: "Yes" }));
    await waitFor(() => expect(delB.textContent).toBe("Delete"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("closes the confirmation on No or a click outside", async () => {
    renderPanel();
    const delA = within(await row("feat-a")).getByTitle("Remove worktree from disk and delete thread");
    await userEvent.click(delA);
    await userEvent.click(screen.getByRole("button", { name: "No" }));
    expect(screen.queryByText("Delete?")).toBeNull();
    const delB = within(await row("feat-b")).getByTitle("Remove worktree from disk");
    await userEvent.click(delB);
    expect(screen.getByText("Delete?")).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByText("Delete?")).toBeNull();
    expect(removeMock).not.toHaveBeenCalled();
  });

  it("highlights the confirmation buttons on hover", async () => {
    renderPanel();
    await userEvent.click(within(await row("feat-b")).getByTitle("Remove worktree from disk"));
    const yes = screen.getByRole("button", { name: "Yes" });
    fireEvent.mouseEnter(yes);
    expect(yes.style.color).toBe(rgb(darkColors.white));
    expect(yes.style.background).toBe(rgb(darkColors.dangerHoverBg));
    fireEvent.mouseLeave(yes);
    expect(yes.style.color).toBe(rgb(darkColors.dangerText));
    expect(yes.style.background).toBe(rgb(darkColors.dangerBg));
    const no = screen.getByRole("button", { name: "No" });
    fireEvent.mouseEnter(no);
    expect(no.style.color).toBe(rgb(darkColors.textLight));
    expect(no.style.borderColor).toBe(rgb(darkColors.textDim));
    fireEvent.mouseLeave(no);
    expect(no.style.color).toBe(rgb(darkColors.textDim));
    expect(no.style.borderColor).toBe(rgb(darkColors.border));
  });
});
