import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RootEntry } from "../../api/files";
import { PaneRootSelect } from "./PaneRootSelect";

const roots: RootEntry[] = [{ index: 0, path: "/w", name: "w" }];

function addSlot(leafId: string) {
  const slot = document.createElement("div");
  slot.id = `pane-header-slot-${leafId}`;
  document.body.appendChild(slot);
  return slot;
}

describe("PaneRootSelect", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  it("portals the root picker into the pane's header slot", () => {
    const slot = addSlot("leaf-1");
    render(<PaneRootSelect leafId="leaf-1" roots={roots} value={0} onChange={vi.fn()} testId="root-pick" title="Root" />);
    expect(slot.textContent).toContain("root");
    expect(slot.contains(screen.getByTestId("root-pick"))).toBe(true);
  });

  it("waits a frame for a slot that mounts later", () => {
    let frame: FrameRequestCallback | undefined;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frame = cb;
      return 1;
    });
    render(<PaneRootSelect leafId="leaf-2" roots={roots} value={0} onChange={vi.fn()} testId="root-pick" />);
    expect(screen.queryByTestId("root-pick")).toBeNull();
    const slot = addSlot("leaf-2");
    act(() => frame!(0));
    expect(slot.contains(screen.getByTestId("root-pick"))).toBe(true);
  });

  it("cancels the pending frame on unmount", () => {
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(42);
    const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const { unmount } = render(<PaneRootSelect leafId="leaf-3" roots={roots} value={0} onChange={vi.fn()} testId="root-pick" />);
    unmount();
    expect(cancel).toHaveBeenCalledWith(42);
  });
});
