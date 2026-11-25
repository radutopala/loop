import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { forkThread } from "../../api/channels";
import { NotificationsContext } from "../../notifications/useNotifications";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import type { PillKind } from "./pills";
import { ThreadItem, type ThreadReorder } from "./ThreadItem";

vi.mock("../../api/channels", () => ({ forkThread: vi.fn() }));
vi.mock("../../api/git", () => ({ fetchPR: vi.fn().mockResolvedValue({ present: false }) }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn().mockResolvedValue(null) }));

const forkMock = vi.mocked(forkThread);

const thread = (fields: Partial<Channel> = {}) => channel({ id: "t1", name: "thread one", parent_id: "p", ...fields });

type Props = Parameters<typeof ThreadItem>[0];

function renderThread(props: Partial<Props> = {}, unread: Record<string, number> = {}) {
  const onSelect = vi.fn();
  const onContextMenu = vi.fn();
  const el: ReactElement = (
    <NotificationsContext.Provider value={{ unread, epoch: 0, pluginsEpoch: 0 }}>
      <ThreadItem thread={thread()} selected={false} onSelect={onSelect} onContextMenu={onContextMenu} {...props} />
    </NotificationsContext.Provider>
  );
  const view = render(el);
  return { ...view, onSelect, onContextMenu };
}

const row = () => screen.getAllByTestId("sidebar-thread-row")[0]!;
const rowButton = () => row().querySelector("button")!;
const pillLabels = () => [...row().querySelectorAll<HTMLElement>("[data-pill]")].map((p) => p.dataset.pill);
const pillMargin = (label: string) => row().querySelector<HTMLElement>(`[data-pill='${label}']`)!.style.marginLeft;
const dots = () => [...rowButton().querySelectorAll<HTMLElement>("span")].filter((s) => s.style.borderRadius === "50%");

function pillsRef(entries: [PillKind, string[]][]) {
  return { current: new Map(entries.map(([k, ids]) => [k, new Set(ids)])) };
}

describe("ThreadItem", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => vi.restoreAllMocks());

  it("shows the session name and selects the thread on click", () => {
    const { onSelect } = renderThread({ thread: thread({ name: "⏱ task #4 nightly", task_id: 4 }) });
    expect(screen.getByText("task #4 nightly")).toBeTruthy();
    fireEvent.click(rowButton());
    expect(onSelect).toHaveBeenCalledWith("t1");
  });

  it("hands its context menu to the sidebar", () => {
    const { onContextMenu } = renderThread();
    fireEvent.contextMenu(rowButton());
    expect(onContextMenu).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ id: "t1" }));
  });

  it("draws a through line for a sibling that isn't last, and a half line for the last", () => {
    const { container, unmount } = renderThread({ isLast: false });
    expect(container.querySelectorAll("line")).toHaveLength(2);
    unmount();
    const last = renderThread({ isLast: true });
    const lines = [...last.container.querySelectorAll("line")];
    expect(lines.map((l) => l.getAttribute("y2"))).toEqual(["50%", "50%"]);
    expect(lines[1]!.getAttribute("x2")).toBe("14");
  });

  it("styles a selected row and leaves its unread dot off", () => {
    renderThread({ selected: true, unreadIdsRef: { current: new Set(["t1"]) } });
    expect(row().style.background).toBe(css(darkColors.selectedBg));
    expect(rowButton().style.color).toBe(css(darkColors.textLight));
    expect(dots()).toHaveLength(0);
    expect(screen.getByText("thread one").style.fontWeight).toBe("600");
  });

  it("shows the unread dot, pushed right when there's no diff", () => {
    renderThread({ unreadIdsRef: { current: new Set(["t1"]) } });
    expect(dots()).toHaveLength(1);
    expect(dots()[0]!.style.marginLeft).toBe("auto");
  });

  it.each<[string, Partial<Channel>, string]>([
    ["additions", { diff_additions: 3 }, "+3"],
    ["deletions", { diff_deletions: 2 }, "-2"],
    ["both", { diff_additions: 3, diff_deletions: 2 }, "+3 -2"],
  ])("shows the diff's %s", (_, fields, text) => {
    renderThread({ thread: thread(fields), unreadIdsRef: { current: new Set(["t1"]) } });
    expect(rowButton().textContent).toContain(text);
    expect(dots()[0]!.style.marginLeft).toBe("4px");
  });

  describe("pills", () => {
    it("shows the lit pills, then review, fixing, plugin badges and unread notes", () => {
      renderThread(
        {
          thread: thread({
            review_running: true,
            pr_attention: "fixing",
            plugin_badges: [
              { plugin_id: "a", text: "A", title: "first" },
              { plugin_id: "b", text: "B" },
            ],
          }),
          pillsRef: pillsRef([
            ["gate", ["t1"]],
            ["ci", ["other"]],
          ]),
        },
        { t1: 3 },
      );
      expect(pillLabels()).toEqual(["gate", "rev…", "ci…", "A", "B", "3"]);
      expect(pillMargin("gate")).toBe("auto");
      for (const label of ["rev…", "ci…", "A", "B", "3"]) expect(pillMargin(label)).toBe("4px");
      expect(row().querySelector<HTMLElement>("[data-pill='A']")!.title).toBe("a: first");
      expect(row().querySelector<HTMLElement>("[data-pill='B']")!.title).toBe("b: B");
      // A review counts as running.
      expect(dots()).toHaveLength(1);
      expect(dots()[0]!.style.marginLeft).toBe("4px");
    });

    it.each<[string, Partial<Channel>, string]>([
      ["review", { review_running: true }, "rev…"],
      ["fixing", { pr_attention: "fixing" }, "ci…"],
      ["badge", { plugin_badges: [{ plugin_id: "a", text: "A" }] }, "A"],
    ])("pushes a lone %s pill right", (_, fields, label) => {
      renderThread({ thread: thread(fields) });
      expect(pillMargin(label)).toBe("auto");
    });

    it("pushes a lone notes pill right, and hides it on the open row", () => {
      const { unmount } = renderThread({}, { t1: 120 });
      expect(pillMargin("99+")).toBe("auto");
      unmount();
      renderThread({ selected: true }, { t1: 2 });
      expect(pillLabels()).toEqual([]);
    });

    it.each<[string, Partial<Props>, Partial<Channel>]>([
      ["unread", { unreadIdsRef: { current: new Set(["t1"]) } }, {}],
      ["a lit pill", { pillsRef: pillsRef([["ask", ["t1"]]]) }, {}],
      ["a diff", {}, { diff_deletions: 1 }],
      ["an addition", {}, { diff_additions: 1 }],
    ])("spaces the trailing pills after %s", (_, props, fields) => {
      renderThread({ ...props, thread: thread({ review_running: true, pr_attention: "fixing", plugin_badges: [{ plugin_id: "a", text: "A" }], ...fields }) }, { t1: 1 });
      for (const label of ["rev…", "ci…", "A", "1"]) expect(pillMargin(label)).toBe("4px");
    });

    it("spaces the notes pill after a badge", () => {
      renderThread({ thread: thread({ plugin_badges: [{ plugin_id: "a", text: "A" }] }) }, { t1: 1 });
      expect(pillMargin("A")).toBe("auto");
      expect(pillMargin("1")).toBe("4px");
    });

    it("spaces fixing and the badges after the review pill", () => {
      renderThread({ thread: thread({ pr_attention: "fixing", plugin_badges: [{ plugin_id: "a", text: "A" }] }) }, { t1: 1 });
      expect(pillMargin("ci…")).toBe("auto");
      expect(pillMargin("A")).toBe("4px");
      expect(pillMargin("1")).toBe("4px");
    });
  });

  it.each<[string, Partial<Props>, Partial<Channel>, string]>([
    ["its container", {}, { container_running: true }, "auto"],
    ["its agent", {}, { agent_running: true }, "auto"],
    ["the chat store", { isRunningMapRef: { current: new Map([["t1", "running"]]) } }, {}, "auto"],
    ["its agent beside a diff", {}, { agent_running: true, diff_additions: 1 }, "4px"],
    ["its agent beside a deletion", {}, { agent_running: true, diff_deletions: 1 }, "4px"],
    ["its agent beside a pill", { pillsRef: pillsRef([["gate", ["t1"]]]) }, { agent_running: true }, "4px"],
    ["its agent on an unread row", { unreadIdsRef: { current: new Set(["t1"]) } }, { agent_running: true }, "4px"],
  ])("shows a running dot from %s", (_, props, fields, margin) => {
    renderThread({ ...props, thread: thread(fields) });
    const running = dots().filter((d) => d.style.backgroundColor === css(darkColors.active));
    expect(running).toHaveLength(1);
    expect(running[0]!.style.marginLeft).toBe(margin);
  });

  describe("select mode", () => {
    it("toggles the thread's check without selecting it", () => {
      const onToggleCheck = vi.fn();
      const { onSelect } = renderThread({ selectMode: true, onToggleCheck });
      const box = rowButton().querySelector("span")!;
      expect(box.querySelector("svg")).toBeNull();
      fireEvent.click(box);
      expect(onToggleCheck).toHaveBeenCalledWith("t1");
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("ticks a checked thread and tolerates no toggle handler", () => {
      renderThread({ selectMode: true, checked: true });
      const box = rowButton().querySelector("span")!;
      expect(box.querySelector("polyline")).toBeTruthy();
      fireEvent.click(box);
    });

    it("offers no fork while picking rows", () => {
      renderThread({ selectMode: true });
      fireEvent.mouseEnter(row());
      expect(screen.queryByText("+fork")).toBeNull();
    });
  });

  describe("fork", () => {
    it("forks the thread on hover's +fork and opens the fork", async () => {
      forkMock.mockResolvedValue("t2");
      const { onSelect } = renderThread();
      fireEvent.mouseEnter(row());
      const fork = screen.getByText("+fork");
      expect(fork.title).toBe("Fork: new thread continuing this conversation");
      fireEvent.mouseEnter(fork);
      expect(fork.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(fork);
      expect(fork.style.color).toBe(css(darkColors.textDim));
      // Leaving +fork leaves the row too; come back to it.
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByText("+fork"));
      expect(forkMock).toHaveBeenCalledWith("t1");
      await waitFor(() => expect(onSelect).toHaveBeenCalledWith("t2"));
    });

    it("ignores clicks while a fork is in flight, then allows another", async () => {
      let finish!: (id: string) => void;
      forkMock.mockReturnValueOnce(new Promise((r) => (finish = r)));
      renderThread({ thread: thread({ worktree: true }) });
      fireEvent.mouseEnter(row());
      const fork = screen.getByText("+fork");
      expect(fork.title).toBe("Fork: new worktree from this branch + continue this conversation");
      fireEvent.click(fork);
      expect(fork.style.opacity).toBe("0.4");
      fireEvent.click(fork);
      expect(forkMock).toHaveBeenCalledOnce();
      await act(async () => finish("t2"));
      expect(fork.style.opacity).toBe("1");
    });

    it("logs a failed fork", async () => {
      forkMock.mockRejectedValue(new Error("nope"));
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const { onSelect } = renderThread();
      fireEvent.mouseEnter(row());
      fireEvent.click(screen.getByText("+fork"));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[sidebar] fork thread failed:", expect.any(Error)));
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("hides +fork when the pointer leaves", () => {
      renderThread();
      fireEvent.mouseEnter(row());
      fireEvent.mouseMove(row());
      fireEvent.mouseLeave(row());
      expect(screen.queryByText("+fork")).toBeNull();
      expect(row().style.background).toBe("transparent");
    });
  });

  describe("sub-threads", () => {
    const subs = [thread({ id: "s1", name: "sub one", parent_id: "t1" }), thread({ id: "s2", name: "sub two", parent_id: "t1" })];

    it("nests its sub-threads and collapses them from the chevron", () => {
      const { onSelect, container } = renderThread({ subThreads: subs, threadsByParent: { s1: [thread({ id: "g1", name: "grandchild", parent_id: "s1" })] }, selectedId: "s2" });
      expect(screen.getByText("sub one")).toBeTruthy();
      expect(screen.getByText("grandchild")).toBeTruthy();
      expect(screen.getAllByTestId("sidebar-thread-row")[3]!.style.background).toBe(css(darkColors.selectedBg));
      // The elbow stops short of the chevron.
      expect(container.querySelector("line[x2='4']")).toBeTruthy();
      const chevron = row().querySelector<HTMLElement>("span[style*='cursor: pointer']")!;
      fireEvent.click(chevron);
      expect(onSelect).not.toHaveBeenCalled();
      expect(screen.queryByText("sub one")).toBeNull();
      expect(chevron.querySelector("svg")!.style.transform).toBe("rotate(-90deg)");
      fireEvent.click(chevron);
      expect(screen.getByText("sub one")).toBeTruthy();
    });

    it("renders sub-threads without a by-parent map", () => {
      renderThread({ subThreads: subs });
      expect(screen.getAllByTestId("sidebar-thread-row")).toHaveLength(3);
    });
  });

  describe("reorder", () => {
    const reorder = (dragOverId: string | null = null): ThreadReorder => ({
      onDragStart: vi.fn(),
      onDragOver: vi.fn(),
      onDrop: vi.fn(),
      onDragEnd: vi.fn(),
      dragOverId,
    });

    it("isn't draggable without reorder wiring", () => {
      renderThread();
      expect(rowButton().getAttribute("draggable")).toBe("false");
      expect(row().style.boxShadow).toBe("");
    });

    it("reports each drag phase with the thread and its parent", () => {
      const r = reorder("t1");
      renderThread({ reorder: r });
      const btn = rowButton();
      expect(btn.getAttribute("draggable")).toBe("true");
      expect(row().style.boxShadow).toContain("inset");
      fireEvent.dragStart(btn);
      fireEvent.dragOver(btn);
      fireEvent.drop(btn);
      fireEvent.dragEnd(btn);
      expect(r.onDragStart).toHaveBeenCalledWith("t1", "p");
      expect(r.onDragOver).toHaveBeenCalledWith(expect.anything(), "t1", "p");
      expect(r.onDrop).toHaveBeenCalledWith(expect.anything(), "t1", "p");
      expect(r.onDragEnd).toHaveBeenCalledOnce();
    });
  });
});
