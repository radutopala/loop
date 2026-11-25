import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageOrigin } from "./MessageOrigin";

// A ResizeObserver stand-in that records its observer so a test can fire it.
class FakeResizeObserver {
  static last: FakeResizeObserver | undefined;
  observed: Element[] = [];
  disconnected = false;
  constructor(readonly cb: () => void) {
    FakeResizeObserver.last = this;
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

function setWidths(el: HTMLElement, scroll: number, client: number) {
  Object.defineProperty(el, "scrollWidth", { configurable: true, value: scroll });
  Object.defineProperty(el, "clientWidth", { configurable: true, value: client });
}

describe("MessageOrigin", () => {
  beforeEach(() => vi.stubGlobal("ResizeObserver", FakeResizeObserver));
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("links to the sending thread with a native title while it fits", () => {
    const { unmount } = render(<MessageOrigin name="build" href="#/c/t1" />);
    const link = screen.getByRole("link", { name: "from #build" });
    expect(link.getAttribute("href")).toBe("#/c/t1");
    expect(link.getAttribute("title")).toBe("Sent by the agent in build: go to the message it was working on");
    expect(FakeResizeObserver.last!.observed).toEqual([link]);
    unmount();
    expect(FakeResizeObserver.last!.disconnected).toBe(true);
  });

  it("swaps the title for a hover tip naming the whole thread once the label is cut", () => {
    vi.useFakeTimers();
    render(<MessageOrigin name="a-very-long-thread-name" href="#x" />);
    const link = screen.getByTestId("message-origin");
    setWidths(link, 300, 100);
    act(() => FakeResizeObserver.last!.cb());
    expect(link.getAttribute("title")).toBeNull();
    fireEvent.mouseEnter(link.parentElement!);
    act(() => vi.advanceTimersByTime(250));
    expect(screen.getByRole("tooltip").textContent).toBe("from #a-very-long-thread-nameSent by the agent in this thread: go to the message it was working on");
  });
});
