import { act, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { learnProposal, learnView, mountPaneHeaderSlot } from "../../test/chat-bits-sidebar-views";
import { LearnBadge, paneHeaderSlotRef, usePaneHeaderSlot } from "./LearnBadge";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("LearnBadge", () => {
  it("renders nothing until the pane header has a slot", () => {
    render(<LearnBadge leafId="l1" learn={learnView({ learnChannelId: "lc" })} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("learn-badge")).toBeNull();
  });

  it("renders nothing for a channel with no learn thread or proposals while closed", () => {
    mountPaneHeaderSlot("l1");
    render(<LearnBadge leafId="l1" learn={learnView()} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("learn-badge")).toBeNull();
  });

  it.each([
    ["a learn thread", { learnChannelId: "lc" }],
    ["proposals", { proposals: [learnProposal()] }],
  ])("shows in the slot for a channel with %s and opens the view", async (_name, over) => {
    const slot = mountPaneHeaderSlot("l1");
    const onToggle = vi.fn();
    render(<LearnBadge leafId="l1" learn={learnView(over)} open={false} onToggle={onToggle} />);
    const badge = screen.getByTestId("learn-badge");
    expect(slot.contains(badge)).toBe(true);
    expect(badge.getAttribute("title")).toBe("Open the Learn view");
    expect(badge.getAttribute("aria-expanded")).toBe("false");
    await userEvent.click(badge);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("shows while open even without a learn thread, as a close button", () => {
    mountPaneHeaderSlot("l1");
    render(<LearnBadge leafId="l1" learn={learnView()} open onToggle={vi.fn()} />);
    const badge = screen.getByTestId("learn-badge");
    expect(badge.getAttribute("title")).toBe("Close the Learn view");
    expect(badge.getAttribute("aria-expanded")).toBe("true");
  });
});

describe("usePaneHeaderSlot", () => {
  it("finds a slot already in the document", () => {
    const slot = mountPaneHeaderSlot("l1");
    const { result } = renderHook(() => usePaneHeaderSlot("l1"));
    expect(result.current).toBe(slot);
  });

  it("looks again after paint when the slot isn't in yet", () => {
    let frame: FrameRequestCallback | undefined;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frame = cb;
      return 7;
    });
    const { result } = renderHook(() => usePaneHeaderSlot("l1"));
    expect(result.current).toBeNull();
    const slot = mountPaneHeaderSlot("l1");
    act(() => frame!(0));
    expect(result.current).toBe(slot);
  });

  it("picks up a slot its header announces later, ignoring other panes'", () => {
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(1);
    const { result } = renderHook(() => usePaneHeaderSlot("l1"));
    const other = mountPaneHeaderSlot("l2");
    act(() => paneHeaderSlotRef(other));
    expect(result.current).toBeNull();
    const slot = mountPaneHeaderSlot("l1");
    act(() => paneHeaderSlotRef(slot));
    expect(result.current).toBe(slot);
  });

  it("follows a new slot after the first one is replaced", () => {
    const first = mountPaneHeaderSlot("l1");
    const { result } = renderHook(() => usePaneHeaderSlot("l1"));
    expect(result.current).toBe(first);
    first.remove();
    const second = mountPaneHeaderSlot("l1");
    act(() => paneHeaderSlotRef(second));
    expect(result.current).toBe(second);
  });

  it("cancels the retry and stops listening on unmount", () => {
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(9);
    const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const remove = vi.spyOn(window, "removeEventListener");
    const { unmount } = renderHook(() => usePaneHeaderSlot("l1"));
    unmount();
    expect(cancel).toHaveBeenCalledWith(9);
    expect(remove).toHaveBeenCalledWith("pane-header-slot", expect.any(Function));
  });

  it("stops listening on unmount when the slot was found at once", () => {
    mountPaneHeaderSlot("l1");
    const remove = vi.spyOn(window, "removeEventListener");
    const { unmount } = renderHook(() => usePaneHeaderSlot("l1"));
    unmount();
    expect(remove).toHaveBeenCalledWith("pane-header-slot", expect.any(Function));
  });
});

describe("paneHeaderSlotRef", () => {
  it("announces nothing for an unmounted slot", () => {
    const dispatch = vi.spyOn(window, "dispatchEvent");
    paneHeaderSlotRef(null);
    expect(dispatch).not.toHaveBeenCalled();
  });
});
