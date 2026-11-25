import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CanvasLearnDock, LEARN_DOCK_GAP, trackMouse } from "./CanvasLearnDock";
import type { CanvasTile } from "./types";

// The animated logo draws with SVG path measuring jsdom lacks.
vi.mock("../components/LoopInfinityIcon", () => ({
  LoopInfinityIcon: ({ animated }: { animated?: boolean }) => <span data-testid="loop-icon" data-animated={String(!!animated)} />,
}));

const chat: CanvasTile = { id: "chat", panel: "chat", x: 100, y: 50, width: 400, height: 300, zIndex: 3 };

function pane() {
  return (
    <div>
      <div data-learn-pane-header>
        <span>Learn header</span>
        <button type="button">Close learn</button>
      </div>
      <p>Learn body</p>
    </div>
  );
}

function renderDock(props: Partial<React.ComponentProps<typeof CanvasLearnDock>> = {}) {
  const onMove = vi.fn();
  const onResize = vi.fn();
  const onBringToFront = vi.fn();
  render(<CanvasLearnDock chat={chat} zoom={1} shown ms={200} running={false} pane={pane()} onMove={onMove} onResize={onResize} onBringToFront={onBringToFront} {...props} />);
  return { onMove, onResize, onBringToFront };
}

describe("CanvasLearnDock", () => {
  it("docks the Learn pane right of the chat's tile at its size, fading in", () => {
    renderDock();
    const dock = screen.getByRole("region", { name: "Learn view" });
    expect(dock.dataset.view).toBe("learn");
    expect(dock.style.left).toBe(`${chat.x + chat.width + LEARN_DOCK_GAP}px`);
    expect(dock.style.top).toBe("50px");
    expect(dock.style.width).toBe("400px");
    expect(dock.style.height).toBe("300px");
    expect(dock.style.zIndex).toBe("3");
    expect(dock.style.opacity).toBe("1");
    expect(dock.style.transition).toBe("opacity 200ms ease");
    expect(screen.getByText("Learn body")).toBeTruthy();
    const logo = screen.getByTestId("canvas-learn-dock-logo");
    expect(logo.style.left).toBe(`${chat.x + chat.width + LEARN_DOCK_GAP / 2}px`);
    expect(logo.style.top).toBe(`${chat.y + chat.height / 2}px`);
    expect(screen.getByTestId("loop-icon").dataset.animated).toBe("false");
  });

  it("fades out without a transition when hidden with no animation, labelled for Explain", () => {
    renderDock({ shown: false, ms: 0, view: "explain", running: true });
    const dock = screen.getByTestId("canvas-learn-dock");
    expect(dock.dataset.view).toBe("explain");
    expect(dock.getAttribute("aria-label")).toBe("Explain view");
    expect(dock.style.opacity).toBe("0");
    expect(dock.style.transition).toBe("none");
    expect(screen.getByTestId("canvas-learn-dock-logo").style.opacity).toBe("0");
    expect(screen.getByTestId("loop-icon").dataset.animated).toBe("true");
  });

  it("moves the chat's tile when its header is dragged, in world units", () => {
    const { onMove, onBringToFront } = renderDock({ zoom: 2 });
    fireEvent.mouseDown(screen.getByText("Learn header"), { button: 0, clientX: 10, clientY: 10 });
    expect(onBringToFront).toHaveBeenCalledWith("chat");
    fireEvent.mouseMove(document, { clientX: 30, clientY: 50 });
    expect(onMove).toHaveBeenCalledWith("chat", 10, 20);
    fireEvent.mouseUp(document);
    fireEvent.mouseMove(document, { clientX: 100, clientY: 100 });
    expect(onMove).toHaveBeenCalledTimes(1);
  });

  it.each([
    { name: "outside the header", target: "Learn body", button: 0 },
    { name: "on a header button", target: "Close learn", button: 0 },
    { name: "with another button", target: "Learn header", button: 2 },
  ])("only brings the pair to the front on a mousedown $name", ({ target, button }) => {
    const { onMove, onBringToFront } = renderDock();
    fireEvent.mouseDown(screen.getByText(target), { button, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(document, { clientX: 40, clientY: 40 });
    expect(onBringToFront).toHaveBeenCalledWith("chat");
    expect(onMove).not.toHaveBeenCalled();
  });

  it("resizes both from the corner, half the width change each, not below the minimum", () => {
    const { onResize, onBringToFront } = renderDock();
    const corner = screen.getByTestId("canvas-learn-dock").lastElementChild as HTMLElement;
    fireEvent.mouseDown(corner, { clientX: 0, clientY: 0 });
    expect(onBringToFront).toHaveBeenCalledWith("chat");
    fireEvent.mouseMove(document, { clientX: 40, clientY: 10 });
    expect(onResize).toHaveBeenLastCalledWith("chat", 420, 310);
    fireEvent.mouseMove(document, { clientX: -1000, clientY: -1000 });
    expect(onResize).toHaveBeenLastCalledWith("chat", 200, 120);
    fireEvent.mouseUp(document);
  });
});

describe("trackMouse", () => {
  it("reports each move since the last, over zoom, until the mouse is released", () => {
    const onDelta = vi.fn();
    trackMouse({ clientX: 0, clientY: 0 }, 0.5, onDelta);
    fireEvent.mouseMove(document, { clientX: 5, clientY: 10 });
    fireEvent.mouseMove(document, { clientX: 6, clientY: 10 });
    fireEvent.mouseUp(document);
    fireEvent.mouseMove(document, { clientX: 50, clientY: 50 });
    expect(onDelta.mock.calls).toEqual([
      [10, 20],
      [2, 0],
    ]);
  });
});
