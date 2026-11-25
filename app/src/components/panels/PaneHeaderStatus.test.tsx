import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { darkColors } from "../../theme";
import type { SessionStatus } from "../../types";
import { PaneHeaderStatus } from "./PaneHeaderStatus";

function addSlot(leafId: string) {
  const slot = document.createElement("div");
  slot.id = `pane-header-slot-${leafId}`;
  document.body.appendChild(slot);
  return slot;
}

// jsdom normalizes colors to rgb(); compare through a probe element.
function cssColor(c: string) {
  const probe = document.createElement("span");
  probe.style.backgroundColor = c;
  return probe.style.backgroundColor;
}

describe("PaneHeaderStatus", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  it.each([
    ["connecting", "Connecting", darkColors.warning, "0.75"],
    ["running", "Running", darkColors.active, "1"],
    ["completed", "Completed", darkColors.textDim, "0.75"],
    ["failed", "Failed", darkColors.error, "0.75"],
  ] as [SessionStatus, string, string, string][])("shows %s as %s with its dot color", (status, label, color, opacity) => {
    const slot = addSlot("l");
    render(<PaneHeaderStatus leafId="l" status={status} elapsed={0} />);
    const text = screen.getByText(label);
    expect(slot.contains(text)).toBe(true);
    const dot = text.previousElementSibling as HTMLElement;
    expect(dot.style.backgroundColor).toBe(cssColor(color));
    expect(dot.style.opacity).toBe(opacity);
  });

  it("shows the elapsed time as hh:mm:ss on hover only", () => {
    addSlot("l");
    render(<PaneHeaderStatus leafId="l" status="running" elapsed={3725} />);
    const pill = screen.getByText("Running").parentElement!;
    expect(screen.queryByText("01:02:05")).toBeNull();
    fireEvent.mouseEnter(pill);
    expect(screen.getByText("01:02:05")).toBeTruthy();
    fireEvent.mouseLeave(pill);
    expect(screen.queryByText("01:02:05")).toBeNull();
  });

  it("shows no elapsed tooltip on hover before any time has passed", () => {
    addSlot("l");
    render(<PaneHeaderStatus leafId="l" status="connecting" elapsed={0} />);
    const pill = screen.getByText("Connecting").parentElement!;
    fireEvent.mouseEnter(pill);
    expect(pill.textContent).toBe("Connecting");
  });

  it("waits a frame for a slot that mounts later", () => {
    let frame: FrameRequestCallback | undefined;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frame = cb;
      return 1;
    });
    render(<PaneHeaderStatus leafId="late" status="running" elapsed={0} />);
    expect(screen.queryByText("Running")).toBeNull();
    const slot = addSlot("late");
    act(() => frame!(0));
    expect(slot.textContent).toBe("Running");
  });

  it("cancels the pending frame on unmount", () => {
    vi.spyOn(window, "requestAnimationFrame").mockReturnValue(9);
    const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
    const { unmount } = render(<PaneHeaderStatus leafId="none" status="running" elapsed={0} />);
    unmount();
    expect(cancel).toHaveBeenCalledWith(9);
  });
});
