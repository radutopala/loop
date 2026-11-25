import { act, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deleteQueuedMessage, reorderQueuedMessages, steerQueuedMessage } from "../../api/loopApi";
import type { Message } from "../../types";
import { QueuedMessagesPopup } from "./QueuedMessagesPopup";

vi.mock("../../api/loopApi", () => ({
  deleteQueuedMessage: vi.fn(),
  reorderQueuedMessages: vi.fn(),
  steerQueuedMessage: vi.fn(),
}));

const deleteMock = vi.mocked(deleteQueuedMessage);
const reorderMock = vi.mocked(reorderQueuedMessages);
const steerMock = vi.mocked(steerQueuedMessage);

function msg(id: string, patch: Partial<Message> = {}): Message {
  return { id: 0, channel_id: "ch-1", msg_id: id, author_id: "u", author_name: "me", content: `text ${id}`, is_bot: false, is_processed: false, created_at: "2026-01-01T00:00:00Z", ...patch };
}

type Props = Parameters<typeof QueuedMessagesPopup>[0];

async function open(props: Partial<Props> = {}) {
  const user = userEvent.setup(vi.isFakeTimers() ? { advanceTimers: vi.advanceTimersByTime } : {});
  const all: Props = { messages: [msg("a"), msg("b"), msg("c")], channelId: "ch-1", ...props };
  const r = render(<QueuedMessagesPopup {...all} />);
  await user.click(screen.getByTestId("queued-toggle"));
  return { user, ...r, rerender: (p: Partial<Props>) => r.rerender(<QueuedMessagesPopup {...all} {...p} />) };
}

const rows = () => screen.getAllByTestId("queued-row");
const contents = () => rows().map((r) => within(r).getByTitle(/Click to/).textContent);
const row = (id: string) => rows().find((r) => within(r).getByTitle(/Click to/).textContent === `text ${id}`)!;
const dt = () => ({ dropEffect: "", effectAllowed: "" });

/** Fires a dragover or drop at a height: jsdom has no DragEvent, so the
 *  pointer's clientY is set on the event by hand. */
function dragAt(kind: "dragOver" | "drop", el: HTMLElement, clientY: number, dataTransfer = dt()) {
  const ev = createEvent[kind](el, { dataTransfer });
  Object.defineProperty(ev, "clientY", { value: clientY });
  fireEvent(el, ev);
}

/** Drags row `from` onto row `to`'s top (before) or bottom (after) half. */
function drag(from: string, to: string, pos: "before" | "after") {
  const handle = within(row(from)).getByTitle("Drag to reorder");
  fireEvent.dragStart(handle, { dataTransfer: dt() });
  const target = row(to);
  const clientY = pos === "before" ? -1 : 1;
  dragAt("dragOver", target, clientY, dt());
  dragAt("drop", target, clientY, dt());
}

describe("QueuedMessagesPopup", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    deleteMock.mockResolvedValue(undefined as never);
    reorderMock.mockResolvedValue(undefined as never);
    steerMock.mockResolvedValue(undefined as never);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders nothing for an empty queue", () => {
    const { container } = render(<QueuedMessagesPopup messages={[]} channelId="ch-1" />);
    expect(container.firstChild).toBeNull();
  });

  it("shows the count and opens and closes the list", async () => {
    const { user } = await open();
    expect(screen.getByTestId("queued-toggle").textContent).toBe("3 queued");
    expect(contents()).toEqual(["text a", "text b", "text c"]);
    await user.click(screen.getByTestId("queued-toggle"));
    expect(screen.queryAllByTestId("queued-row")).toHaveLength(0);
  });

  it("expands and collapses a row", async () => {
    const { user } = await open();
    const text = within(row("a")).getByTitle("Click to expand");
    await user.click(text);
    expect(text.title).toBe("Click to collapse");
    expect(text.style.whiteSpace).toBe("pre-wrap");
    await user.click(text);
    expect(text.title).toBe("Click to expand");
  });

  it("shows a delayed row's countdown", async () => {
    await open({ messages: [msg("a", { not_before: Date.now() / 1000 + 90 })] });
    expect(within(row("a")).getByTitle("Scheduled — runs when the countdown ends")).toBeTruthy();
  });

  describe("copy", () => {
    it("copies the text and marks the row copied for a moment", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const { user } = await open();
      const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
      const btn = within(row("b")).getByTitle("Copy to clipboard");
      fireEvent.mouseEnter(btn);
      expect(btn.style.color).not.toBe("");
      await user.click(btn);
      expect(writeText).toHaveBeenCalledWith("text b");
      expect(btn.title).toBe("Copied");
      const lit = btn.style.color;
      fireEvent.mouseEnter(btn);
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).toBe(lit);
      act(() => vi.advanceTimersByTime(1200));
      expect(btn.title).toBe("Copy to clipboard");
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).not.toBe(lit);
    });

    it("stays unmarked when the clipboard refuses", async () => {
      const { user } = await open();
      vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
      const btn = within(row("a")).getByTitle("Copy to clipboard");
      await user.click(btn);
      expect(btn.title).toBe("Copy to clipboard");
    });
  });

  describe("delete", () => {
    it("removes a row, dimmed until the queue drops it", async () => {
      let finish!: () => void;
      deleteMock.mockReturnValue(new Promise<never>((r) => (finish = r as () => void)));
      const { user } = await open({ onEdit: vi.fn(async () => true), isRunning: true });
      const btn = within(row("a")).getByTitle("Remove from queue");
      fireEvent.mouseEnter(btn);
      const hover = btn.style.color;
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).not.toBe(hover);
      await user.click(btn);
      expect(deleteMock).toHaveBeenCalledWith("ch-1", "a");
      expect(row("a").style.opacity).toBe("0.5");
      expect((btn as HTMLButtonElement).disabled).toBe(true);
      // While deleting, hovering its buttons doesn't light them.
      const before = btn.style.color;
      fireEvent.mouseEnter(btn);
      expect(btn.style.color).toBe(before);
      const edit = within(row("a")).getByTestId("queued-edit");
      expect((edit as HTMLButtonElement).disabled).toBe(true);
      expect(edit.style.cursor).toBe("default");
      const editColor = edit.style.color;
      fireEvent.mouseEnter(edit);
      expect(edit.style.color).toBe(editColor);
      expect((within(row("a")).getByTestId("queued-steer") as HTMLButtonElement).disabled).toBe(true);
      await act(async () => finish());
    });

    it("restores the row when the delete fails", async () => {
      deleteMock.mockRejectedValue(new Error("gone"));
      const { user } = await open();
      await user.click(within(row("a")).getByTitle("Remove from queue"));
      await waitFor(() => expect(row("a").style.opacity).toBe("1"));
    });
  });

  describe("edit", () => {
    it("opens a row in the composer and marks it editing", async () => {
      const onEdit = vi.fn(async () => true);
      const { user, rerender } = await open({ onEdit, isRunning: true });
      const edit = within(row("b")).getByTestId("queued-edit");
      fireEvent.mouseEnter(edit);
      const hover = edit.style.color;
      fireEvent.mouseLeave(edit);
      expect(edit.style.color).not.toBe(hover);
      await user.click(edit);
      expect(onEdit).toHaveBeenCalledWith(msg("b"));
      expect(screen.queryByTestId("queued-edit-too-late")).toBeNull();
      rerender({ onEdit, isRunning: true, editingMsgId: "b" });
      const r = row("b");
      expect(r.dataset.editing).toBe("true");
      expect(within(r).getByTestId("queued-editing")).toBeTruthy();
      expect(within(r).queryByTestId("queued-edit")).toBeNull();
      expect(within(r).queryByTestId("queued-steer")).toBeNull();
      const del = within(r).getByTitle("Finish or cancel the edit first");
      expect((del as HTMLButtonElement).disabled).toBe(true);
      const c = del.style.color;
      fireEvent.mouseEnter(del);
      expect(del.style.color).toBe(c);
    });

    it("says the row already started when the edit came too late, for a moment", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const { user } = await open({ onEdit: vi.fn(async () => false) });
      await user.click(within(row("a")).getByTestId("queued-edit"));
      expect(within(row("a")).getByTestId("queued-edit-too-late").textContent).toBe("already started");
      act(() => vi.advanceTimersByTime(2500));
      expect(screen.queryByTestId("queued-edit-too-late")).toBeNull();
    });

    it("logs a failed edit", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const err = new Error("locked");
      const { user } = await open({ onEdit: vi.fn(async () => Promise.reject(err)) });
      await user.click(within(row("a")).getByTestId("queued-edit"));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] editing queued message:", err));
      expect(screen.queryByTestId("queued-edit-too-late")).toBeNull();
    });
  });

  describe("steer", () => {
    it("is offered only while the agent runs", async () => {
      await open();
      expect(screen.queryAllByTestId("queued-steer")).toHaveLength(0);
    });

    it("steers a row to the top and keeps it marked", async () => {
      const { user } = await open({ isRunning: true });
      const btn = within(row("c")).getByTestId("queued-steer");
      expect(btn.title).toBe("Stop the agent and send this next");
      fireEvent.mouseEnter(btn);
      const hover = btn.style.color;
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).not.toBe(hover);
      await user.click(btn);
      expect(steerMock).toHaveBeenCalledWith("ch-1", "c");
      await waitFor(() => expect(contents()).toEqual(["text c", "text a", "text b"]));
      const steered = within(row("c")).getByTestId("queued-steer");
      expect(steered.dataset.steered).toBe("true");
      expect(steered.textContent).toBe("Steered");
      expect(steered.title).toBe("Steered: it goes next");
      expect((steered as HTMLButtonElement).disabled).toBe(true);
      const c = steered.style.color;
      fireEvent.mouseEnter(steered);
      fireEvent.mouseLeave(steered);
      expect(steered.style.color).toBe(c);
    });

    it("makes it steerable again when the steer fails", async () => {
      steerMock.mockRejectedValue(new Error("no run"));
      const { user } = await open({ isRunning: true });
      await user.click(within(row("b")).getByTestId("queued-steer"));
      await waitFor(() => expect(within(row("b")).getByTestId("queued-steer").textContent).toBe("Steer"));
      expect(contents()).toEqual(["text a", "text b", "text c"]);
    });

    it("steering one added row marks every added row", async () => {
      const { user } = await open({ isRunning: true, onEdit: vi.fn(async () => true), messages: [msg("x", { adding: true }), msg("y", { adding: true }), msg("a")] });
      const x = row("x");
      expect(x.dataset.adding).toBe("true");
      expect(within(x).getByTestId("queued-adding")).toBeTruthy();
      expect(within(x).queryByTestId("queued-edit")).toBeNull();
      expect(within(x).queryByTitle("Remove from queue")).toBeNull();
      expect(within(x).queryByTitle("Drag to reorder")).toBeNull();
      const btn = within(x).getByTestId("queued-steer");
      expect(btn.title).toBe("Stop the agent and have it read all added messages now");
      await user.click(btn);
      await waitFor(() => expect(within(row("y")).getByTestId("queued-steer").dataset.steered).toBe("true"));
      expect(within(row("x")).getByTestId("queued-steer").title).toBe("Steered: the agent reads it now");
      expect(within(row("a")).getByTestId("queued-steer").dataset.steered).toBeUndefined();
    });

    it("names a single added row", async () => {
      await open({ isRunning: true, messages: [msg("x", { adding: true }), msg("a")] });
      expect(within(row("x")).getByTestId("queued-steer").title).toBe("Stop the agent and have it read this now");
    });
  });

  describe("drag to reorder", () => {
    it("moves a row below another and saves the order", async () => {
      await open();
      drag("a", "c", "after");
      expect(contents()).toEqual(["text b", "text c", "text a"]);
      expect(reorderMock).toHaveBeenCalledWith("ch-1", ["b", "c", "a"]);
    });

    it("moves a row above another", async () => {
      await open();
      drag("c", "a", "before");
      expect(contents()).toEqual(["text c", "text a", "text b"]);
      expect(reorderMock).toHaveBeenCalledWith("ch-1", ["c", "a", "b"]);
    });

    it("draws the drop line on the hovered edge, and clears it when the drag ends", async () => {
      await open();
      const handle = within(row("a")).getByTitle("Drag to reorder");
      const data = dt();
      fireEvent.dragStart(handle, { dataTransfer: data });
      expect(data.effectAllowed).toBe("move");
      const over = dt();
      dragAt("dragOver", row("b"), -1, over);
      expect(over.dropEffect).toBe("move");
      expect(row("b").style.boxShadow).toContain("inset 0 2px");
      dragAt("dragOver", row("b"), -1, dt());
      expect(row("b").style.boxShadow).toContain("inset 0 2px");
      dragAt("dragOver", row("b"), 1, dt());
      expect(row("b").style.boxShadow).toContain("inset 0 -2px");
      expect(row("a").style.boxShadow).toBe("");
      fireEvent.dragEnd(handle);
      expect(row("b").style.boxShadow).toBe("");
      // A drop after the drag ended moves nothing.
      dragAt("drop", row("c"), 1, dt());
      expect(reorderMock).not.toHaveBeenCalled();
    });

    it("ignores a drop that changes nothing", async () => {
      await open();
      drag("a", "a", "after");
      drag("a", "b", "before");
      expect(contents()).toEqual(["text a", "text b", "text c"]);
      expect(reorderMock).not.toHaveBeenCalled();
    });

    it("ignores a drop of a row that left the queue meanwhile", async () => {
      const { rerender } = await open();
      fireEvent.dragStart(within(row("a")).getByTitle("Drag to reorder"), { dataTransfer: dt() });
      rerender({ messages: [msg("b"), msg("c")] });
      dragAt("drop", row("c"), 1, dt());
      expect(reorderMock).not.toHaveBeenCalled();
    });

    it("keeps added rows on top, out of the saved order, and can't drop on them", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      reorderMock.mockRejectedValue(new Error("stale"));
      const { rerender } = await open({ messages: [msg("x", { adding: true }), msg("a"), msg("b")] });
      const x = row("x");
      const over = dt();
      fireEvent.dragStart(within(row("b")).getByTitle("Drag to reorder"), { dataTransfer: dt() });
      dragAt("dragOver", x, -1, over);
      expect(over.dropEffect).toBe("");
      dragAt("drop", x, -1, dt());
      expect(reorderMock).not.toHaveBeenCalled();
      dragAt("drop", row("a"), -1, dt());
      expect(contents()).toEqual(["text x", "text b", "text a"]);
      expect(reorderMock).toHaveBeenCalledWith("ch-1", ["b", "a"]);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] reordering queued messages:", expect.any(Error)));
      // The local order holds as the queue changes: a gone row drops out,
      // a new one goes last, and added rows stay first.
      rerender({ messages: [msg("a"), msg("d"), msg("y", { adding: true })] });
      expect(contents()).toEqual(["text y", "text a", "text d"]);
    });
  });
});
