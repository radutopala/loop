import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type MemoryFileInfo, type SearchMessageResult, searchMemoryFiles, searchMessages } from "../../api/loopApi";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { CommandPalette } from "./CommandPalette";

vi.mock("../../api/loopApi", () => ({ searchMessages: vi.fn(), searchMemoryFiles: vi.fn() }));

const messagesMock = vi.mocked(searchMessages);
const memoryMock = vi.mocked(searchMemoryFiles);

function channel(patch: Partial<Channel>): Channel {
  return { id: "", name: "", parent_id: "", dir_path: "", session_id: "", active: true, container_running: false, agent_running: false, branch: "", commit: "", worktree: false, ...patch } as Channel;
}

const loop = channel({ id: "c1", name: "loop", dir_path: "/src/loop", branch: "main" });
const unnamed = channel({ id: "c2", dir_path: "/src/other" });
const thread = channel({ id: "t1", name: "fix tests", parent_id: "c1" });
const orphan = channel({ id: "t2", name: "orphan", parent_id: "gone" });
const channels = [loop, thread, unnamed, orphan];

const message = (patch: Partial<SearchMessageResult>): SearchMessageResult => ({ id: 1, channel_id: "c1", author_name: "ann", content: "hello", is_bot: false, created_at: "", ...patch });
const memFile: MemoryFileInfo = { file_path: "/mem/notes.md", dir_path: "/mem" };

type Props = Parameters<typeof CommandPalette>[0];

function renderPalette(props: Partial<Props> = {}) {
  const handlers = { onClose: vi.fn(), onSelect: vi.fn(), onSelectMessage: vi.fn(), onSelectMemoryFile: vi.fn() };
  const all: Props = { channels, selectedChannelId: "c1", open: true, ...handlers, ...props };
  const utils = render(<CommandPalette {...all} />);
  const input = screen.getByPlaceholderText("Search channels, threads, messages, and memory...") as HTMLInputElement;
  return { ...utils, ...handlers, input, all };
}

// rows lists the palette's rows (items and section headers) as text.
function rows(): string[] {
  const list = screen.getByPlaceholderText(/Search channels/).parentElement!.nextElementSibling!;
  return [...list.children].map((c) => c.textContent ?? "");
}

async function type(input: HTMLInputElement, value: string) {
  fireEvent.change(input, { target: { value } });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(300);
  });
}

describe("CommandPalette", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    messagesMock.mockResolvedValue([]);
    memoryMock.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders nothing while closed", () => {
    const { container } = render(<CommandPalette channels={channels} open={false} onClose={vi.fn()} onSelect={vi.fn()} />);
    expect(container.innerHTML).toBe("");
  });

  it("lists channels then threads, focusing the input on open", async () => {
    const { input } = renderPalette();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(document.activeElement).toBe(input);
    expect(rows()).toEqual(["#loop/src/loop | main", "#/src/other/src/other", "┗fix testsloop › thread", "┗orphanthread"]);
  });

  it("fuzzy-filters by label or detail and says when nothing matches", async () => {
    const { input } = renderPalette();
    await type(input, "lp");
    expect(rows()).toEqual(["#loop/src/loop | main", "┗fix testsloop › thread"]);
    await type(input, "zzzz");
    expect(rows()).toEqual(["No results"]);
  });

  it("searches messages and memory after a pause, naming each message's channel", async () => {
    messagesMock.mockResolvedValue([
      message({ id: 5, content: "line one\nline two", channel_id: "c1" }),
      message({ id: 6, content: "x".repeat(90), channel_id: "c2" }),
      message({ id: 7, content: "elsewhere", channel_id: "unknown" }),
    ]);
    memoryMock.mockResolvedValue([memFile]);
    const { input } = renderPalette();
    fireEvent.change(input, { target: { value: "lo" } });
    expect(rows()).toContain("Searching messages...");
    expect(messagesMock).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(messagesMock).toHaveBeenCalledWith("lo", 10);
    expect(memoryMock).toHaveBeenCalledWith("c1", "lo");
    expect(rows()).toEqual([
      "#loop/src/loop | main",
      "┗fix testsloop › thread",
      "Memory",
      "\u{1F9E0}notes.md/mem",
      "Messages",
      "\u{1F4AC}line one line twoann in loop",
      `\u{1F4AC}${"x".repeat(80)}…ann in /src/other`,
      "\u{1F4AC}elsewhereann in unknown",
    ]);
  });

  it("starts the sections without a divider when no channel matches", async () => {
    messagesMock.mockResolvedValue([message({ content: "qqq" })]);
    memoryMock.mockResolvedValue([memFile]);
    const { input } = renderPalette();
    await type(input, "qqq");
    const memoryHeader = screen.getByText("Memory");
    const messagesHeader = screen.getByText("Messages");
    expect(memoryHeader.style.borderTop).toBe("");
    expect(memoryHeader.style.marginTop).toBe("0px");
    expect(messagesHeader.style.borderTop).toContain("1px solid");
    expect(messagesHeader.style.marginTop).toBe("4px");
  });

  it("puts only messages under a divider-less header without channels or memory", async () => {
    messagesMock.mockResolvedValue([message({ content: "qqq" })]);
    const { input } = renderPalette({ selectedChannelId: null });
    await type(input, "qqq");
    expect(memoryMock).not.toHaveBeenCalled();
    expect(screen.getByText("Messages").style.borderTop).toBe("");
    expect(screen.getByText("Messages").style.marginTop).toBe("0px");
  });

  it("drops results when the searches fail or the query gets short", async () => {
    messagesMock.mockResolvedValueOnce([message({ content: "qqq" })]);
    memoryMock.mockResolvedValueOnce([memFile]);
    const { input } = renderPalette();
    await type(input, "qqq");
    expect(screen.getByText("Messages")).toBeTruthy();

    messagesMock.mockRejectedValueOnce(new Error("down"));
    memoryMock.mockRejectedValueOnce(new Error("down"));
    await type(input, "qqqq");
    expect(rows()).toEqual(["No results"]);

    messagesMock.mockResolvedValueOnce([message({ content: "qqq" })]);
    await type(input, "qqq");
    expect(screen.getByText("Messages")).toBeTruthy();
    await type(input, "q");
    expect(rows()).toEqual(["No results"]);
  });

  it("debounces: only the last query searches", async () => {
    const { input } = renderPalette();
    fireEvent.change(input, { target: { value: "ab" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    fireEvent.change(input, { target: { value: "abc" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(messagesMock).toHaveBeenCalledOnce();
    expect(messagesMock).toHaveBeenCalledWith("abc", 10);
  });

  it("moves the selection with the arrows and hover, picking with Enter", async () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const { input, onSelect, onClose } = renderPalette();
    const selected = () =>
      rows().findIndex((_, i) => (screen.getByPlaceholderText(/Search channels/).parentElement!.nextElementSibling!.children[i] as HTMLElement).style.backgroundColor === css(darkColors.selectedBg));
    expect(selected()).toBe(0);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selected()).toBe(3);
    expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(selected()).toBe(2);
    fireEvent.keyDown(input, { key: "Tab" });
    expect(selected()).toBe(2);
    fireEvent.mouseEnter(screen.getByText("loop"));
    expect(selected()).toBe(0);
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(selected()).toBe(0);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onSelect).toHaveBeenCalledWith("c1");
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("does nothing on Enter without results, and survives ArrowDown there", async () => {
    const { input, onSelect, onClose } = renderPalette({ channels: [] });
    expect(rows()).toEqual(["No results"]);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onSelect).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("closes on Escape and on a click outside, not inside", () => {
    const { input, onClose, container } = renderPalette();
    fireEvent.click(input);
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
    fireEvent.click(container.firstElementChild!);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("opens a picked memory file and jumps to a picked message, with hover selection", async () => {
    messagesMock.mockResolvedValue([message({ id: 9, content: "qqq", channel_id: "c2" })]);
    memoryMock.mockResolvedValue([memFile]);
    const { input, onSelectMemoryFile, onSelectMessage, onClose } = renderPalette();
    await type(input, "qqq");
    const memRow = screen.getByText("notes.md").parentElement!.parentElement!;
    const msgRow = screen.getByText("qqq").parentElement!.parentElement!;
    fireEvent.mouseEnter(memRow);
    expect(memRow.style.backgroundColor).toBe(css(darkColors.selectedBg));
    fireEvent.mouseEnter(msgRow);
    expect(msgRow.style.backgroundColor).toBe(css(darkColors.selectedBg));
    expect(memRow.style.backgroundColor).toBe("transparent");
    fireEvent.click(memRow);
    expect(onSelectMemoryFile).toHaveBeenCalledWith("/mem/notes.md");
    fireEvent.click(msgRow);
    expect(onSelectMessage).toHaveBeenCalledWith("c2", 9);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("opens a clicked thread", () => {
    const { onSelect, onClose } = renderPalette();
    fireEvent.click(screen.getByText("fix tests"));
    expect(onSelect).toHaveBeenCalledWith("t1");
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("opens a message's channel without a message handler, and only closes for a memory file without one", async () => {
    messagesMock.mockResolvedValue([message({ id: 9, content: "qqq", channel_id: "c2" })]);
    memoryMock.mockResolvedValue([memFile]);
    const { input, onSelect, onClose } = renderPalette({ onSelectMessage: undefined, onSelectMemoryFile: undefined });
    await type(input, "qqq");
    fireEvent.click(screen.getByText("qqq"));
    expect(onSelect).toHaveBeenCalledWith("c2");
    fireEvent.click(screen.getByText("notes.md"));
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("resets the query when reopened", async () => {
    const { input, rerender, all } = renderPalette();
    await type(input, "loop");
    rerender(<CommandPalette {...all} open={false} />);
    rerender(<CommandPalette {...all} open />);
    expect((screen.getByPlaceholderText(/Search channels/) as HTMLInputElement).value).toBe("");
  });
});
