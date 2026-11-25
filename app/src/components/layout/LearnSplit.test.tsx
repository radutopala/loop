import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../../ThemeContext";
import { LearnSplit, SplitSeamLogo } from "./LearnSplit";

// The animated logo walks its SVG path, which jsdom can't measure.
vi.mock("../LoopInfinityIcon", () => ({
  LoopInfinityIcon: ({ animated, color }: { animated: boolean; color?: string }) => <span data-testid="logo-icon" data-animated={String(animated)} data-color={color ?? ""} />,
}));

type Props = Parameters<typeof LearnSplit>[0];

function renderSplit(props: Partial<Props> = {}) {
  return render(<LearnSplit shown ms={0} running={false} chatHeader={<div>chat header</div>} chat={<div>the chat</div>} learnPane={<div>the learn pane</div>} {...props} />);
}

/** The view's divider, after giving the view a box (jsdom has no layout). */
function dragDivider() {
  const view = screen.getByRole("region");
  vi.spyOn(view, "getBoundingClientRect").mockReturnValue({ left: 100, top: 0, width: 1000, height: 600 } as DOMRect);
  const bar = view.querySelector<HTMLElement>('[style*="cursor: col-resize"]');
  if (!bar) throw new Error("no resize handle");
  return bar;
}

describe("LearnSplit", () => {
  it("lays the chat, its header and the side pane out as a named region", () => {
    renderSplit({ view: "explain" });
    const view = screen.getByRole("region", { name: "Explain view" });
    expect(view.dataset.view).toBe("explain");
    const chatHalf = screen.getByTestId("learn-split-chat");
    expect(chatHalf.textContent).toBe("chat headerthe chat");
    expect(screen.getByText("the learn pane")).toBeTruthy();
    expect(chatHalf.parentElement!.style.width).toMatch(/^calc\(50% \+ \d+px\)$/);
  });

  it("takes a label and test id over the view's", () => {
    renderSplit({ label: "Shell of ch-1", testId: "side" });
    expect(screen.getByRole("region", { name: "Shell of ch-1" })).toBeTruthy();
    expect(screen.getByTestId("side-chat")).toBeTruthy();
    expect(screen.getByTestId("side-logo")).toBeTruthy();
  });

  it("gives the chat its share of the width", () => {
    renderSplit({ split: 0.3 });
    expect(screen.getByTestId("learn-split-chat").parentElement!.style.width).toMatch(/^calc\(30% \+ \d+px\)$/);
    expect(screen.getByTestId("learn-split-logo").style.left).toBe("30%");
  });

  it("has no resize handle without onSplitChange", () => {
    renderSplit();
    expect(document.querySelector('[style*="cursor: col-resize"]')).toBeNull();
  });

  it("reports the chat's new share while the divider is dragged, clamped, until the mouse is up", () => {
    const onSplitChange = vi.fn();
    renderSplit({ onSplitChange });
    fireEvent.mouseDown(dragDivider(), { clientX: 600 });
    expect(document.body.style.cursor).toBe("col-resize");

    fireEvent.mouseMove(document, { clientX: 400 });
    expect(onSplitChange).toHaveBeenLastCalledWith(0.3);
    fireEvent.mouseMove(document, { clientX: 1100 });
    expect(onSplitChange).toHaveBeenLastCalledWith(0.85);
    fireEvent.mouseMove(document, { clientX: 0 });
    expect(onSplitChange).toHaveBeenLastCalledWith(0.15);

    fireEvent.mouseUp(document);
    expect(document.body.style.cursor).toBe("");
    onSplitChange.mockClear();
    fireEvent.mouseMove(document, { clientX: 500 });
    expect(onSplitChange).not.toHaveBeenCalled();
  });

  it("passes its box to the caller's ref", () => {
    const ref = vi.fn();
    renderSplit({ ref });
    expect(ref).toHaveBeenCalledWith(screen.getByRole("region"));
  });

  it.each([
    { shown: true, ms: 150, opacity: "1", transition: "opacity 150ms ease" },
    { shown: false, ms: 150, opacity: "0", transition: "opacity 150ms ease" },
    { shown: false, ms: 0, opacity: "0", transition: "none" },
  ])("fades the side pane and logo (shown $shown, $ms ms)", ({ shown, ms, opacity, transition }) => {
    renderSplit({ shown, ms });
    const logo = screen.getByTestId("learn-split-logo");
    expect(logo.style.opacity).toBe(opacity);
    expect(logo.style.transition).toBe(transition);
    const sideHalf = screen.getByText("the learn pane").parentElement!.parentElement!;
    expect(sideHalf.style.opacity).toBe(opacity);
  });

  it("leaves the logo out with noLogo", () => {
    renderSplit({ noLogo: true });
    expect(screen.queryByTestId("learn-split-logo")).toBeNull();
  });

  it("passes its box to an object ref", () => {
    const ref = { current: null as HTMLDivElement | null };
    renderSplit({ ref });
    expect(ref.current).toBe(screen.getByRole("region"));
  });

  it("keeps frames from taking the mouse while the divider is dragged", () => {
    const frame = document.createElement("iframe");
    document.body.appendChild(frame);
    renderSplit({ onSplitChange: vi.fn() });
    fireEvent.mouseDown(dragDivider(), { clientX: 600 });
    expect(frame.style.pointerEvents).toBe("none");
    fireEvent.mouseUp(document);
    expect(frame.style.pointerEvents).toBe("");
    frame.remove();
  });

  it("falls back to a 4px seam when the theme sets no island gap", () => {
    render(
      <ThemeProvider initialIslands={false}>
        <LearnSplit shown ms={0} running={false} chatHeader={null} chat={null} learnPane={null} />
      </ThemeProvider>,
    );
    expect(screen.getByTestId("learn-split-chat").parentElement!.style.width).toBe("calc(50% + 2px)");
  });

  it("puts a seam logo on its own at the middle by default", () => {
    render(<SplitSeamLogo testId="seam" running={false} style={{ opacity: 0.5 }} />);
    const logo = screen.getByTestId("seam");
    expect(logo.style.left).toBe("50%");
    expect(logo.style.opacity).toBe("0.5");
  });

  it("animates the seam logo in its own colors while running, dim otherwise", () => {
    const { rerender } = render(<SplitSeamLogo testId="seam" running />);
    expect(screen.getByTestId("logo-icon").dataset).toMatchObject({ animated: "true", color: "" });
    rerender(<SplitSeamLogo testId="seam" running={false} />);
    expect(screen.getByTestId("logo-icon").dataset.animated).toBe("false");
    expect(screen.getByTestId("logo-icon").dataset.color).not.toBe("");
  });
});
