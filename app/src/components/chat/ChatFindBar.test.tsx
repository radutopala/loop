import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { searchChannelMessages } from "../../api/search";
import { ChatFindBar } from "./ChatFindBar";

vi.mock("../../api/search", () => ({ searchChannelMessages: vi.fn() }));

const searchMock = vi.mocked(searchChannelMessages);

function renderBar(focusKey = 0) {
  const onJump = vi.fn();
  const onClose = vi.fn();
  const utils = render(<ChatFindBar channelId="ch-1" focusKey={focusKey} onJump={onJump} onClose={onClose} />);
  const input = screen.getByTestId("chat-find-input") as HTMLInputElement;
  return { ...utils, onJump, onClose, input };
}

// Types the term and lets the debounced search run and land.
async function search(input: HTMLInputElement, term: string) {
  fireEvent.change(input, { target: { value: term } });
  await act(async () => vi.advanceTimersByTime(250));
}

describe("ChatFindBar", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    searchMock.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("selects its input when asked to open again", () => {
    const { input, rerender, onJump, onClose } = renderBar(1);
    fireEvent.change(input, { target: { value: "abc" } });
    input.setSelectionRange(3, 3);
    rerender(<ChatFindBar channelId="ch-1" focusKey={2} onJump={onJump} onClose={onClose} />);
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 3]);
  });

  it("searches once typing pauses and jumps to the newest match", async () => {
    searchMock.mockResolvedValue([30, 20, 10]);
    const { input, onJump } = renderBar();
    fireEvent.change(input, { target: { value: "f" } });
    act(() => vi.advanceTimersByTime(100));
    await search(input, "  foo ");
    expect(searchMock).toHaveBeenCalledOnce();
    expect(searchMock).toHaveBeenCalledWith("ch-1", "foo", expect.any(AbortSignal));
    expect(onJump).toHaveBeenCalledWith(30, "foo");
    expect(screen.getByTestId("chat-find-count").textContent).toBe("1 / 3");
  });

  it("steps through the matches with the buttons and Enter, wrapping around", async () => {
    searchMock.mockResolvedValue([30, 20, 10]);
    const { input, onJump } = renderBar();
    await search(input, "foo");
    fireEvent.click(screen.getByTestId("chat-find-older"));
    expect(onJump).toHaveBeenLastCalledWith(20, "foo");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onJump).toHaveBeenLastCalledWith(10, "foo");
    expect(screen.getByTestId("chat-find-count").textContent).toBe("3 / 3");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onJump).toHaveBeenLastCalledWith(30, "foo");
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(onJump).toHaveBeenLastCalledWith(10, "foo");
    fireEvent.click(screen.getByTestId("chat-find-newer"));
    expect(onJump).toHaveBeenLastCalledWith(20, "foo");
  });

  it("says when nothing matches and keeps the step buttons off", async () => {
    searchMock.mockResolvedValue([]);
    const { input, onJump } = renderBar();
    await search(input, "zzz");
    expect(screen.getByTestId("chat-find-count").textContent).toBe("no matches");
    expect((screen.getByTestId("chat-find-older") as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId("chat-find-newer") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onJump).not.toHaveBeenCalled();
  });

  it("ignores Enter before any search and other keys", () => {
    const { input, onJump, onClose } = renderBar();
    fireEvent.keyDown(input, { key: "Enter" });
    fireEvent.keyDown(input, { key: "a" });
    expect(onJump).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("treats a failed search as no matches", async () => {
    searchMock.mockRejectedValue(new Error("down"));
    const { input } = renderBar();
    await search(input, "foo");
    expect(screen.getByTestId("chat-find-count").textContent).toBe("no matches");
  });

  it("drops a search the next term replaced", async () => {
    let fail!: (e: Error) => void;
    searchMock.mockReturnValueOnce(new Promise((_, reject) => (fail = reject))).mockResolvedValue([5]);
    const { input, onJump } = renderBar();
    await search(input, "foo");
    await search(input, "foob");
    expect(onJump).toHaveBeenCalledWith(5, "foob");
    await act(async () => fail(new Error("aborted")));
    expect(screen.getByTestId("chat-find-count").textContent).toBe("1 / 1");
  });

  it("clears the count when the term is emptied", async () => {
    searchMock.mockResolvedValue([5]);
    const { input } = renderBar();
    await search(input, "foo");
    fireEvent.change(input, { target: { value: "  " } });
    expect(screen.queryByTestId("chat-find-count")).toBeNull();
  });

  it("closes on Escape and on the close button", () => {
    const { input, onClose } = renderBar();
    fireEvent.keyDown(input, { key: "Escape" });
    fireEvent.click(screen.getByTestId("chat-find-close"));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
