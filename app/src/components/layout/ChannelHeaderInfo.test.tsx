import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchCommits } from "../../api/git";
import { useChannelPR } from "../../hooks/useChannelPR";
import { layoutChannel } from "../../test/layout-fixtures";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { openExternalUrl } from "../../utils/openExternal";
import { ChannelHeaderInfo } from "./ChannelHeaderInfo";

vi.mock("../../api/git", () => ({ fetchCommits: vi.fn() }));
vi.mock("../../hooks/useChannelPR", () => ({ useChannelPR: vi.fn() }));
vi.mock("../../utils/openExternal", () => ({ openExternalUrl: vi.fn() }));

const commitsMock = vi.mocked(fetchCommits);
const prMock = vi.mocked(useChannelPR);
const openMock = vi.mocked(openExternalUrl);
const writeText = vi.fn();

function renderInfo(over: Partial<Channel> = {}, hideBranch?: boolean) {
  return render(<ChannelHeaderInfo channel={layoutChannel(over)} colors={darkColors} hideBranch={hideBranch} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  prMock.mockReturnValue(null);
  commitsMock.mockResolvedValue([]);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
});

afterEach(() => {
  delete (navigator as { clipboard?: unknown }).clipboard;
  vi.restoreAllMocks();
});

describe("ChannelHeaderInfo", () => {
  it("renders nothing without a directory", () => {
    const { container } = renderInfo({ dir_path: "" });
    expect(container.firstChild).toBeNull();
  });

  it("shows a channel's path, id and that it has no session", () => {
    renderInfo({ display_path: "~/proj" });
    expect(screen.getByText("~/proj").title).toBe("/home/u/proj\nDouble-click to copy path");
    expect(screen.getByLabelText("Channel").textContent).toBe("#");
    expect(screen.getByText("ch-1").title).toBe("Channel ch-1\nDouble-click to copy");
    expect(screen.getByText("no session")).toBeTruthy();
    expect(screen.queryByTestId("header-ticket")).toBeNull();
    expect(screen.queryByTestId("header-pr")).toBeNull();
    expect(screen.queryByLabelText("Commit")).toBeNull();
    expect(prMock).toHaveBeenCalledWith("ch-1", "");
  });

  it("shows the raw path without a display one, and a thread's icon", () => {
    renderInfo({ parent_id: "ch-0" });
    expect(screen.getByText("/home/u/proj")).toBeTruthy();
    expect(screen.getByLabelText("Thread")).toBeTruthy();
    expect(screen.getByText("ch-1").title).toBe("Thread ch-1\nDouble-click to copy");
  });

  it.each([
    { text: "/home/u/proj", copied: "/home/u/proj" },
    { text: "ch-1", copied: "ch-1" },
    { text: "sess-9", copied: "sess-9" },
    { text: "abc1234", copied: "abc1234" },
    { text: "feature/x", copied: "feature/x" },
  ])("copies $text on double-click and selects it", ({ text, copied }) => {
    const selectAll = vi.fn();
    vi.spyOn(window, "getSelection").mockReturnValue({ selectAllChildren: selectAll } as unknown as Selection);
    renderInfo({ session_id: "sess-9", commit: "abc1234", branch: "feature/x" });
    const el = screen.getByText(text, { selector: "span" });
    fireEvent.doubleClick(el);
    expect(writeText).toHaveBeenCalledWith(copied);
    expect(selectAll).toHaveBeenCalledWith(el);
  });

  it("copies even without a selection to update", () => {
    vi.spyOn(window, "getSelection").mockReturnValue(null);
    renderInfo({ session_id: "sess-9", commit: "abc1234", branch: "main" });
    for (const t of ["/home/u/proj", "ch-1", "sess-9", "abc1234", "main"]) fireEvent.doubleClick(screen.getByText(t, { selector: "span" }));
    expect(writeText).toHaveBeenCalledTimes(5);
  });

  it("opens the ticket and the PR on click", () => {
    prMock.mockReturnValue({ number: 12, url: "https://forge.example/pr/12", base_ref: "main", head_ref: "x", state: "OPEN", title: "Fix" });
    renderInfo({ ticket_url: "  https://tickets.example/browse/ABC-7  ", branch: "x" });
    const ticket = screen.getByTestId("header-ticket");
    expect(ticket.textContent).toBe("ABC-7");
    fireEvent.click(ticket);
    expect(openMock).toHaveBeenCalledWith("https://tickets.example/browse/ABC-7");
    const pr = screen.getByTestId("header-pr");
    expect(pr.textContent).toBe("PR #12");
    expect(pr.title).toBe("PR #12 Fix (open)\nhttps://forge.example/pr/12\nClick to open");
    expect(screen.getByTestId("header-pr-state").dataset.state).toBe("open");
    fireEvent.click(pr);
    expect(openMock).toHaveBeenCalledWith("https://forge.example/pr/12");
  });

  it("hides the branch when asked, and colors a worktree's", () => {
    const { rerender } = renderInfo({ branch: "feat" }, true);
    expect(screen.queryByText("feat")).toBeNull();
    const color = (hex: string) => {
      const probe = document.createElement("span");
      probe.style.color = hex;
      return probe.style.color;
    };
    rerender(<ChannelHeaderInfo channel={layoutChannel({ branch: "feat" })} colors={darkColors} />);
    expect(screen.getByText("feat").style.color).toBe(color(darkColors.textDim));
    rerender(<ChannelHeaderInfo channel={layoutChannel({ branch: "feat", worktree: true })} colors={darkColors} />);
    expect(screen.getByText("feat").style.color).toBe(color(darkColors.active));
  });

  it("flags a worktree still at its base branch's tip", async () => {
    commitsMock.mockResolvedValue([{ hash: "abc1234def", short: "abc1234", subject: "", author: "", date: "" }]);
    renderInfo({ worktree: true, base_branch: "main", commit: "abc1234", branch: "wt" });
    expect(await screen.findByText("= main")).toBeTruthy();
    expect(commitsMock).toHaveBeenCalledWith("ch-1", "main", 1);
  });

  it("matches the base's tip by a hash prefix", async () => {
    commitsMock.mockResolvedValue([{ hash: "abc1234def", short: "zzz", subject: "", author: "", date: "" }]);
    renderInfo({ worktree: true, base_branch: "main", commit: "abc12" });
    expect(await screen.findByText("= main")).toBeTruthy();
  });

  it("doesn't flag a worktree past its base, or a base without commits", async () => {
    commitsMock.mockResolvedValueOnce([{ hash: "fff", short: "fff", subject: "", author: "", date: "" }]);
    const { rerender } = renderInfo({ worktree: true, base_branch: "main", commit: "abc" });
    await waitFor(() => expect(commitsMock).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("= main")).toBeNull();
    commitsMock.mockResolvedValueOnce([]);
    rerender(<ChannelHeaderInfo channel={layoutChannel({ worktree: true, base_branch: "main", commit: "abd" })} colors={darkColors} />);
    await waitFor(() => expect(commitsMock).toHaveBeenCalledTimes(2));
    expect(screen.queryByText("= main")).toBeNull();
  });

  it("drops the flag once the channel's no longer a worktree at its base", async () => {
    commitsMock.mockResolvedValue([{ hash: "abc", short: "abc", subject: "", author: "", date: "" }]);
    const { rerender } = renderInfo({ worktree: true, base_branch: "main", commit: "abc" });
    expect(await screen.findByText("= main")).toBeTruthy();
    rerender(<ChannelHeaderInfo channel={layoutChannel({ worktree: false, base_branch: "main", commit: "abc" })} colors={darkColors} />);
    expect(screen.queryByText("= main")).toBeNull();
  });

  it("ignores a base lookup that comes back after the channel changed, and logs a failed one", async () => {
    let resolve!: (v: Awaited<ReturnType<typeof fetchCommits>>) => void;
    commitsMock.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    const { unmount } = renderInfo({ worktree: true, base_branch: "main", commit: "abc" });
    unmount();
    resolve([{ hash: "abc", short: "abc", subject: "", author: "", date: "" }]);
    await Promise.resolve();

    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    commitsMock.mockRejectedValueOnce(new Error("boom"));
    renderInfo({ worktree: true, base_branch: "main", commit: "abc" });
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching base branch commit:", expect.any(Error)));
    expect(screen.queryByText("= main")).toBeNull();
  });
});
