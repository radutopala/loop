import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type BranchInfo, fetchBranches, fetchOpenPRs, type PRInfo } from "../../api/git";
import { SidebarWorktreeButton } from "./SidebarWorktreeButton";

vi.mock("../../api/git", () => ({ fetchBranches: vi.fn(), fetchOpenPRs: vi.fn() }));

const branchesMock = vi.mocked(fetchBranches);
const prsMock = vi.mocked(fetchOpenPRs);

const info: BranchInfo = {
  branches: ["main", "feature/Login", "fix-bug"],
  current: "main",
  worktrees: [
    { path: "/repo/.wt/login", branch: "feature/Login", thread_id: "th-1" },
    { path: "/repo/.wt/spare", branch: "spare" },
  ],
};
const pr: PRInfo = { number: 7, url: "u", base_ref: "main", head_ref: "feature/Login", state: "OPEN", title: "Add login" };

/** A ResizeObserver stand-in that hands back the callback it was built with. */
class FakeResizeObserver {
  static last: FakeResizeObserver | undefined;
  observed: Element[] = [];
  disconnected = false;
  constructor(readonly callback: () => void) {
    FakeResizeObserver.last = this;
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

function setup(props: Partial<Parameters<typeof SidebarWorktreeButton>[0]> = {}) {
  const onCreateWorktree = vi.fn();
  const user = userEvent.setup();
  render(<SidebarWorktreeButton channelId="ch-1" onCreateWorktree={onCreateWorktree} {...props} />);
  const button = screen.getByTestId("sidebar-worktree-button");
  return { onCreateWorktree, user, button };
}

const picker = () => screen.queryByTestId("sidebar-worktree-picker");

describe("SidebarWorktreeButton", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal("ResizeObserver", FakeResizeObserver);
    branchesMock.mockResolvedValue(info);
    prsMock.mockResolvedValue({ prs: [pr], forge: "github" });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("offers branches only without a PR handler", () => {
    const { button } = setup();
    expect(button.title).toBe("New worktree from branch");
    expect(button.textContent).toBe("+wt");
  });

  it("highlights on hover", () => {
    const { button } = setup();
    fireEvent.mouseEnter(button);
    expect(button.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(button);
    expect(button.style.backgroundColor).toBe("transparent");
  });

  it("opens a portaled picker that loads the branches and focuses its search", async () => {
    let finish!: (b: BranchInfo) => void;
    branchesMock.mockReturnValue(new Promise((r) => (finish = r)));
    const parentClick = vi.fn();
    const onCreateWorktree = vi.fn();
    const user = userEvent.setup();
    render(
      <div onClick={parentClick}>
        <SidebarWorktreeButton channelId="ch-1" onCreateWorktree={onCreateWorktree} />
      </div>,
    );
    await user.click(screen.getByTestId("sidebar-worktree-button"));
    expect(parentClick).not.toHaveBeenCalled();
    expect(branchesMock).toHaveBeenCalledWith("ch-1");
    expect(picker()?.parentElement).toBe(document.body);
    expect(screen.getByText("Loading branches…")).toBeTruthy();
    const search = screen.getByPlaceholderText("Worktree from branch…");
    await waitFor(() => expect(document.activeElement).toBe(search));
    await act(async () => finish({ branches: ["main"], current: "main", worktrees: [] }));
    expect(screen.queryByText("Loading branches…")).toBeNull();
    // A plain picker has no section titles.
    expect(screen.queryByText("Branches")).toBeNull();
    expect(picker()?.style.minWidth).toBe("260px");
    expect(picker()?.style.maxHeight).toBe("min(360px, 60vh)");
    await user.click(screen.getByTitle("New worktree thread from main"));
    expect(parentClick).not.toHaveBeenCalled();
  });

  it("filters the branches by search, ignoring case, and says when none match", async () => {
    const { user, button } = setup();
    await user.click(button);
    await screen.findByText("fix-bug");
    await user.type(screen.getByPlaceholderText("Worktree from branch…"), "LOGIN");
    expect(screen.queryByText("fix-bug")).toBeNull();
    expect(screen.getByTitle("New worktree thread from feature/Login")).toBeTruthy();
    await user.clear(screen.getByPlaceholderText("Worktree from branch…"));
    await user.type(screen.getByPlaceholderText("Worktree from branch…"), "nothing");
    expect(screen.getByText("No branches found")).toBeTruthy();
  });

  it("creates a worktree from the picked branch and closes", async () => {
    const { user, button, onCreateWorktree } = setup();
    await user.click(button);
    const row = await screen.findByTitle("New worktree thread from fix-bug");
    fireEvent.mouseEnter(row);
    expect(row.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(row);
    expect(row.style.backgroundColor).toBe("transparent");
    await user.click(row);
    expect(onCreateWorktree).toHaveBeenCalledWith("ch-1", "fix-bug");
    expect(picker()).toBeNull();
  });

  it("closes on a second click and reopens fresh, with the search cleared", async () => {
    const { user, button } = setup();
    await user.click(button);
    await user.type(screen.getByPlaceholderText("Worktree from branch…"), "fix");
    await user.click(button);
    expect(picker()).toBeNull();
    await user.click(button);
    expect((screen.getByPlaceholderText("Worktree from branch…") as HTMLInputElement).value).toBe("");
    expect(branchesMock).toHaveBeenCalledTimes(2);
  });

  it("closes on a mousedown outside, not on one inside", async () => {
    const { user, button } = setup();
    await user.click(button);
    fireEvent.mouseDown(screen.getByPlaceholderText("Worktree from branch…"));
    fireEvent.mouseDown(button);
    expect(picker()).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(picker()).toBeNull();
  });

  it("logs a failed branch listing and keeps showing it loading", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const err = new Error("no repo");
    branchesMock.mockRejectedValue(err);
    const { user, button } = setup();
    await user.click(button);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching branches for worktree:", err));
    expect(screen.getByText("Loading branches…")).toBeTruthy();
  });

  describe("with PRs and worktrees", () => {
    it("lists open PRs and opens the picked one in its existing worktree", async () => {
      const onOpenPR = vi.fn();
      const { user, button } = setup({ onOpenPR, onSelectThread: vi.fn() });
      expect(button.title).toBe("New worktree from a branch or PR");
      await user.click(button);
      expect(prsMock).toHaveBeenCalledWith("ch-1");
      expect(screen.getByPlaceholderText("Worktree from branch or PR…")).toBeTruthy();
      expect(screen.getByText("Branches")).toBeTruthy();
      // Tall while the PRs load.
      expect(picker()?.style.height).toBe("min(400px, 70vh)");
      const row = await screen.findByTestId("sidebar-worktree-prs-7");
      expect(picker()?.style.minWidth).toBe("340px");
      await user.click(row);
      expect(onOpenPR).toHaveBeenCalledWith("ch-1", pr, info.worktrees[0]);
      expect(picker()).toBeNull();
    });

    it("is short when the repo has no PRs open and no worktrees to list", async () => {
      branchesMock.mockResolvedValue({ ...info, worktrees: [] });
      prsMock.mockResolvedValue({ prs: [], forge: "github" });
      const { user, button } = setup({ onOpenPR: vi.fn() });
      await user.click(button);
      await screen.findByText("No open pull requests");
      expect(screen.getByText("Branches")).toBeTruthy();
      expect(picker()?.style.height).toBe("");
    });

    it("has no PR section for a repo without a forge", async () => {
      prsMock.mockResolvedValue({ prs: [] });
      const { user, button } = setup({ onOpenPR: vi.fn() });
      await user.click(button);
      await screen.findByText("fix-bug");
      await waitFor(() => expect(screen.queryByTestId("sidebar-worktree-prs")).toBeNull());
      expect(screen.queryByText("Branches")).toBeNull();
    });

    it("goes to a worktree's thread and closes", async () => {
      const onSelectThread = vi.fn();
      const { user, button } = setup({ onSelectThread });
      await user.click(button);
      await screen.findByTestId("sidebar-worktree-worktrees");
      expect(prsMock).not.toHaveBeenCalled();
      expect(screen.getByText("Branches")).toBeTruthy();
      expect(picker()?.style.height).toBe("min(400px, 70vh)");
      await user.click(screen.getByTestId("worktree-go"));
      expect(onSelectThread).toHaveBeenCalledWith("th-1");
      expect(picker()).toBeNull();
    });

    it("imports a worktree without a thread as one of the channel's", async () => {
      const onImportWorktree = vi.fn();
      const { user, button } = setup({ onImportWorktree });
      await user.click(button);
      const imports = await screen.findAllByTestId("worktree-import");
      await user.click(imports[1]!);
      expect(onImportWorktree).toHaveBeenCalledWith("ch-1", "/repo/.wt/spare");
      expect(picker()).toBeNull();
    });

    it("lists no worktrees without a way to act on them", async () => {
      const { user, button } = setup();
      await user.click(button);
      await screen.findByText("fix-bug");
      expect(screen.queryByTestId("sidebar-worktree-worktrees")).toBeNull();
    });
  });

  describe("placement", () => {
    const rect = (top: number, left: number) => ({ top, left, bottom: top + 20, right: left + 30, width: 30, height: 20 }) as DOMRect;

    it("opens below the button and follows it on scroll, resize and growth", async () => {
      const { user, button } = setup();
      let at = rect(100, 40);
      button.getBoundingClientRect = () => at;
      await user.click(button);
      const menu = picker() as HTMLElement;
      expect([menu.style.top, menu.style.left]).toEqual(["122px", "40px"]);
      const observer = FakeResizeObserver.last!;
      expect(observer.observed).toEqual([menu]);

      at = rect(200, 50);
      act(() => {
        fireEvent.scroll(document.body);
      });
      expect([menu.style.top, menu.style.left]).toEqual(["222px", "50px"]);

      // The menu's own lists scrolling moves nothing.
      at = rect(300, 50);
      act(() => {
        fireEvent.scroll(screen.getByPlaceholderText("Worktree from branch…"));
      });
      expect(menu.style.top).toBe("222px");

      act(() => {
        fireEvent(window, new Event("resize"));
      });
      expect(menu.style.top).toBe("322px");

      at = rect(10, 50);
      act(() => observer.callback());
      expect(menu.style.top).toBe("32px");
      // Placing it again where it already is keeps it there.
      act(() => observer.callback());
      expect(menu.style.top).toBe("32px");
    });

    it("stops following once closed", async () => {
      const remove = vi.spyOn(window, "removeEventListener");
      const { user, button } = setup();
      await user.click(button);
      const observer = FakeResizeObserver.last!;
      await user.click(button);
      expect(observer.disconnected).toBe(true);
      expect(remove.mock.calls.map((c) => c[0])).toEqual(expect.arrayContaining(["scroll", "resize"]));
    });
  });
});
