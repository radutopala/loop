import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsContext } from "../../notifications/useNotifications";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { ChannelItem } from "./ChannelItem";
import type { PillKind } from "./pills";

vi.mock("../../api/channels", () => ({ forkThread: vi.fn() }));
vi.mock("../../api/git", () => ({ fetchPR: vi.fn().mockResolvedValue({ present: false }), fetchBranches: vi.fn().mockResolvedValue({ branches: [], current: "" }) }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn().mockResolvedValue(null) }));

type Props = Parameters<typeof ChannelItem>[0];

const project = (fields: Partial<Channel> = {}) => channel({ id: "c1", name: "proj", ...fields });
const threadA = channel({ id: "t1", name: "alpha", parent_id: "c1" });
const threadB = channel({ id: "t2", name: "beta", parent_id: "c1" });

function renderItem(props: Partial<Props> = {}, unread: Record<string, number> = {}) {
  const handlers = {
    onSelect: vi.fn(),
    onCreateThread: vi.fn(),
    onContextMenu: vi.fn(),
    onDragStart: vi.fn(),
    onDragOver: vi.fn(),
    onDrop: vi.fn(),
    onDragEnd: vi.fn(),
  };
  const view = render(
    <NotificationsContext.Provider value={{ unread, epoch: 0, pluginsEpoch: 0 }}>
      <ChannelItem channel={project()} threads={[]} threadsByParent={{}} selected={false} selectedId={null} isDragOver={false} {...handlers} {...props} />
    </NotificationsContext.Provider>,
  );
  return { ...view, ...handlers };
}

const row = () => screen.getByTestId("sidebar-channel-row");
const nameButton = () => [...row().querySelectorAll("button")].find((b) => b.textContent?.includes("#"))!;
const pillLabels = () => [...row().querySelectorAll<HTMLElement>("[data-pill]")].map((p) => p.dataset.pill);
const runningDots = () => [...nameButton().querySelectorAll<HTMLElement>("span")].filter((s) => s.style.borderRadius === "50%");

function pillsRef(entries: [PillKind, string[]][]) {
  return { current: new Map(entries.map(([k, ids]) => [k, new Set(ids)])) };
}

describe("ChannelItem", () => {
  beforeEach(() => vi.clearAllMocks());

  it.each<[string, Partial<Channel>, string]>([
    ["its name", {}, "proj"],
    ["its directory's name without a name", { name: "", dir_path: "/src/repo" }, "repo"],
    ["its id without a name or directory", { name: "" }, "c1"],
  ])("shows %s and selects on click", (_, fields, text) => {
    const { onSelect } = renderItem({ channel: project(fields) });
    expect(nameButton().textContent).toContain(text);
    fireEvent.click(nameButton());
    expect(onSelect).toHaveBeenCalledWith("c1");
  });

  it("styles the selected row", () => {
    renderItem({ selected: true });
    expect(row().style.backgroundColor).toBe(css(darkColors.selectedBg));
    expect(nameButton().style.fontWeight).toBe("600");
  });

  it("marks a drop target and reports drags and its context menu", () => {
    const { container, onDragStart, onDragOver, onDrop, onDragEnd, onContextMenu } = renderItem({ isDragOver: true });
    expect(row().style.borderTop).toContain(css(darkColors.active));
    const outer = container.querySelector("[draggable]")!;
    fireEvent.dragStart(outer);
    fireEvent.dragOver(outer);
    fireEvent.drop(outer);
    fireEvent.dragEnd(outer);
    fireEvent.contextMenu(row());
    expect(onDragStart).toHaveBeenCalledWith("c1");
    expect(onDragOver).toHaveBeenCalledWith(expect.anything(), "c1");
    expect(onDrop).toHaveBeenCalledWith(expect.anything(), "c1");
    expect(onDragEnd).toHaveBeenCalledOnce();
    expect(onContextMenu).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ id: "c1" }));
  });

  it("shows lit pills, review, fixing, plugin badges and unread notes", () => {
    renderItem(
      {
        channel: project({ review_running: true, pr_attention: "fixing", plugin_badges: [{ plugin_id: "p", text: "P" }] }),
        pillsRef: pillsRef([["gate", ["c1"]]]),
      },
      { c1: 2 },
    );
    expect(pillLabels()).toEqual(["gate", "rev…", "ci…", "P", "2"]);
    expect(row().querySelector<HTMLElement>("[data-pill='P']")!.title).toBe("p: P");
    expect(runningDots()).toHaveLength(1);
  });

  it("hides the notes count on the open row", () => {
    renderItem({ selected: true }, { c1: 2 });
    expect(pillLabels()).toEqual([]);
  });

  it.each<[string, Partial<Props>, Partial<Channel>]>([
    ["its container", {}, { container_running: true }],
    ["its agent", {}, { agent_running: true }],
    ["the chat store", { isRunningMapRef: { current: new Map([["c1", "running"]]) } }, {}],
  ])("shows a running dot from %s", (_, props, fields) => {
    renderItem({ ...props, channel: project(fields) });
    expect(runningDots()).toHaveLength(1);
  });

  it("shows no running dot when idle", () => {
    renderItem();
    expect(runningDots()).toHaveLength(0);
  });

  it.each<[Partial<Channel>, string]>([
    [{ diff_additions: 3 }, "+3"],
    [{ diff_deletions: 2 }, "-2"],
    [{ diff_additions: 3, diff_deletions: 2 }, "+3 -2"],
  ])("shows the diff %o", (fields, text) => {
    renderItem({ channel: project(fields) });
    expect(nameButton().textContent).toContain(text);
  });

  describe("threads", () => {
    it("lists its threads and collapses them from the chevron", () => {
      renderItem({ threads: [threadA, threadB], threadsByParent: { t1: [channel({ id: "s1", name: "sub", parent_id: "t1" })] }, selectedId: "t2" });
      expect(screen.getByText("alpha")).toBeTruthy();
      expect(screen.getByText("sub")).toBeTruthy();
      const chevron = row().querySelector("button")!;
      fireEvent.click(chevron);
      expect(screen.queryByText("alpha")).toBeNull();
      expect(chevron.querySelector("svg")!.style.transform).toBe("rotate(-90deg)");
      fireEvent.click(chevron);
      expect(screen.getByText("beta")).toBeTruthy();
    });

    it("has a spacer and no chevron without threads", () => {
      renderItem();
      expect(row().querySelectorAll("button")).toHaveLength(1);
    });
  });

  describe("select mode", () => {
    it("toggles the channel's check without selecting it, and checks threads", () => {
      const onToggleCheck = vi.fn();
      const { onSelect } = renderItem({ selectMode: true, checkedIds: new Set(["t1"]), onToggleCheck, threads: [threadA] });
      const box = row().querySelector("span")!;
      expect(box.querySelector("polyline")).toBeNull();
      fireEvent.click(box);
      expect(onToggleCheck).toHaveBeenCalledWith("c1");
      expect(onSelect).not.toHaveBeenCalled();
      const threadRow = screen.getByTestId("sidebar-thread-row");
      expect(threadRow.querySelector("polyline")).toBeTruthy();
    });

    it("ticks a checked channel and tolerates no handler or set", () => {
      const { unmount } = renderItem({ selectMode: true, checkedIds: new Set(["c1"]) });
      expect(row().querySelector("polyline")).toBeTruthy();
      fireEvent.click(row().querySelector("span")!);
      unmount();
      renderItem({ selectMode: true });
      expect(row().querySelector("polyline")).toBeNull();
    });

    it("gives the pinned DM no checkbox", () => {
      renderItem({ selectMode: true, pinned: true, threads: [threadA] });
      expect(row().querySelectorAll("button")).toHaveLength(2);
    });
  });

  describe("hover actions", () => {
    it("shows only + thread for a channel without a directory", () => {
      renderItem({ onOpenConfig: vi.fn(), onCreateWorktree: vi.fn() });
      fireEvent.mouseEnter(row());
      expect(row().style.backgroundColor).toBe(css(darkColors.hoverBg));
      expect(screen.getByTitle("New thread")).toBeTruthy();
      expect(screen.queryByTitle("Project config")).toBeNull();
      expect(screen.queryByTitle(/New worktree/)).toBeNull();
    });

    it("opens the project config and highlights the button under the pointer", () => {
      const onOpenConfig = vi.fn();
      const { onSelect } = renderItem({ channel: project({ dir_path: "/src/repo" }), onOpenConfig });
      fireEvent.mouseEnter(row());
      const config = screen.getByTitle("Project config");
      fireEvent.mouseEnter(config);
      expect(config.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(config);
      expect(config.style.color).toBe(css(darkColors.textDim));
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByTitle("Project config"));
      expect(onOpenConfig).toHaveBeenCalledWith("/src/repo");
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("offers a worktree button for a project with a directory", () => {
      renderItem({ channel: project({ dir_path: "/src/repo" }), onCreateWorktree: vi.fn() });
      fireEvent.mouseEnter(row());
      expect(screen.getByTitle("New worktree from branch")).toBeTruthy();
    });

    it("creates a thread from + thread", async () => {
      const user = userEvent.setup();
      const { onCreateThread } = renderItem();
      fireEvent.mouseEnter(row());
      const add = screen.getByTitle("New thread");
      fireEvent.mouseEnter(add);
      expect(add.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(add);
      expect(add.style.color).toBe(css(darkColors.textDim));
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByTitle("New thread"));
      await user.type(screen.getByPlaceholderText("Thread name..."), "  fresh {Enter}");
      expect(onCreateThread).toHaveBeenCalledWith("c1", "fresh");
      expect(screen.queryByPlaceholderText("Thread name...")).toBeNull();
    });

    it("closes the new thread input on Escape, or on a second + thread", async () => {
      const user = userEvent.setup();
      const { onCreateThread } = renderItem();
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByTitle("New thread"));
      await user.type(screen.getByPlaceholderText("Thread name..."), "x{Escape}");
      expect(screen.queryByPlaceholderText("Thread name...")).toBeNull();
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByTitle("New thread"));
      fireEvent.click(screen.getByTitle("New thread"));
      expect(screen.queryByPlaceholderText("Thread name...")).toBeNull();
      expect(onCreateThread).not.toHaveBeenCalled();
    });
  });
});
