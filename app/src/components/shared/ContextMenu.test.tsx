import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { ContextMenu, type MenuItem } from "./ContextMenu";

function items(...labels: string[]): MenuItem[] {
  return labels.map((label) => ({ label, onClick: vi.fn() }));
}

function renderMenu(menuItems: MenuItem[], x = 10, y = 20) {
  const onClose = vi.fn();
  const utils = render(<ContextMenu x={x} y={y} items={menuItems} onClose={onClose} />);
  return { ...utils, onClose };
}

const key = (k: string) => fireEvent.keyDown(document, { key: k });

describe("ContextMenu", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("opens at the given point, focused, and returns focus to its trigger on close", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const { unmount } = renderMenu(items("Rename", "Delete"));
    const menu = screen.getByTestId("context-menu");
    expect(menu.style.left).toBe("10px");
    expect(menu.style.top).toBe("20px");
    expect(document.activeElement).toBe(menu);
    expect(screen.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Rename", "Delete"]);
    unmount();
    expect(document.activeElement).toBe(trigger);
    trigger.remove();
  });

  it("closes without a trigger to refocus", () => {
    vi.spyOn(document, "activeElement", "get").mockReturnValue(null);
    const { unmount } = renderMenu(items("A"));
    expect(() => unmount()).not.toThrow();
  });

  it("shifts left and up to stay inside the window", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 200, height: 100, bottom: 0 } as DOMRect);
    renderMenu(items("A"), window.innerWidth - 50, window.innerHeight - 20);
    const menu = screen.getByTestId("context-menu");
    expect(menu.style.left).toBe(`${window.innerWidth - 208}px`);
    expect(menu.style.top).toBe(`${window.innerHeight - 108}px`);
  });

  it("keeps a margin from the top-left for a menu bigger than the window", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 5000, height: 5000, bottom: 0 } as DOMRect);
    renderMenu(items("A"), 100, 100);
    const menu = screen.getByTestId("context-menu");
    expect(menu.style.left).toBe("8px");
    expect(menu.style.top).toBe("8px");
  });

  it("runs an item and closes", () => {
    const menuItems = items("Rename");
    const { onClose } = renderMenu(menuItems);
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    expect(menuItems[0]!.onClick).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("walks items with the arrow keys, Home and End, wrapping around", () => {
    renderMenu(items("A", "B", "C"));
    const [a, b, c] = screen.getAllByRole("menuitem");
    key("ArrowDown");
    expect(document.activeElement).toBe(a);
    expect(a!.tabIndex).toBe(0);
    expect(b!.tabIndex).toBe(-1);
    key("ArrowDown");
    expect(document.activeElement).toBe(b);
    key("ArrowUp");
    key("ArrowUp");
    expect(document.activeElement).toBe(c);
    key("ArrowDown");
    expect(document.activeElement).toBe(a);
    key("End");
    expect(document.activeElement).toBe(c);
    key("Home");
    expect(document.activeElement).toBe(a);
    key("x");
    expect(document.activeElement).toBe(a);
  });

  it("starts at the last item on a first ArrowUp", () => {
    renderMenu(items("A", "B"));
    key("ArrowUp");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "B" }));
  });

  it("closes on Escape and ignores navigation keys without items", () => {
    const { onClose } = renderMenu([]);
    key("ArrowDown");
    key("Home");
    expect(onClose).not.toHaveBeenCalled();
    key("Escape");
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("closes on a left click outside, not on another button", () => {
    const { onClose } = renderMenu(items("A"));
    const backdrop = screen.getByTestId("context-menu").previousElementSibling!;
    fireEvent.mouseDown(backdrop, { button: 2 });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.mouseDown(backdrop, { button: 0 });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("hands a right click outside to the element underneath", () => {
    const { onClose } = renderMenu(items("A"));
    const target = document.createElement("div");
    document.body.appendChild(target);
    const received = vi.fn();
    target.addEventListener("contextmenu", (e) => received((e as MouseEvent).clientX, (e as MouseEvent).clientY));
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frames.push(cb);
      return 1;
    });
    const fromPoint = vi.fn<(x: number, y: number) => Element | null>().mockReturnValueOnce(target).mockReturnValueOnce(null);
    Object.defineProperty(document, "elementFromPoint", { value: fromPoint, configurable: true });

    const backdrop = screen.getByTestId("context-menu").previousElementSibling!;
    expect(fireEvent.contextMenu(backdrop, { clientX: 30, clientY: 40 })).toBe(false);
    expect(onClose).toHaveBeenCalledOnce();
    act(() => frames[0]!(0));
    expect(fromPoint).toHaveBeenCalledWith(30, 40);
    expect(received).toHaveBeenCalledWith(30, 40);

    // Nothing underneath: nothing to re-dispatch to.
    fireEvent.contextMenu(backdrop, { clientX: 1, clientY: 2 });
    act(() => frames[1]!(0));
    expect(received).toHaveBeenCalledOnce();

    Reflect.deleteProperty(document, "elementFromPoint");
    target.remove();
  });

  it("swallows a right click on the menu itself", () => {
    renderMenu(items("A"));
    expect(fireEvent.contextMenu(screen.getByTestId("context-menu"))).toBe(false);
  });

  it("draws separators, hints, titles and danger colours", () => {
    renderMenu([
      { label: "Open", onClick: vi.fn(), hint: "~/repo", title: "Open the repo" },
      { label: "Delete", onClick: vi.fn(), danger: true, separator: true },
    ]);
    const open = screen.getByRole("menuitem", { name: /Open/ });
    expect(open.title).toBe("Open the repo");
    expect(open.textContent).toBe("Open~/repo");
    expect(open.getAttribute("aria-checked")).toBeNull();
    const del = screen.getByRole("menuitem", { name: "Delete" });
    expect(del.previousElementSibling).toBeTruthy();
    expect(open.previousElementSibling).toBeNull();
    expect(del.style.color).toBe(css(darkColors.dangerText));
    expect(open.style.color).toBe(css(darkColors.textLight));

    fireEvent.mouseEnter(del);
    expect(del.style.backgroundColor).toBe(css(darkColors.dangerBg));
    fireEvent.mouseLeave(del);
    expect(del.style.backgroundColor).toBe("transparent");
    fireEvent.mouseEnter(open);
    expect(open.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
  });

  it("checks the current choice in a picker", () => {
    renderMenu([
      { label: "Dark", onClick: vi.fn(), checked: true },
      { label: "Light", onClick: vi.fn() },
    ]);
    const [dark, light] = screen.getAllByRole("menuitemradio");
    expect(dark!.getAttribute("aria-checked")).toBe("true");
    expect(dark!.textContent).toBe("✓Dark");
    expect(light!.getAttribute("aria-checked")).toBe("false");
    expect(light!.textContent).toBe("Light");
  });

  it("asks before a confirmed item runs: below with room, yes runs it", () => {
    const onClick = vi.fn();
    const { onClose } = renderMenu([{ label: "Delete", onClick, confirm: "Delete channel?" }]);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ bottom: 10 } as DOMRect);
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(onClick).not.toHaveBeenCalled();
    const pop = screen.getByTestId("context-menu-confirm");
    expect(pop.dataset.side).toBe("below");
    expect(pop.textContent).toContain("Delete channel?");
    fireEvent.click(screen.getByTestId("confirm-yes"));
    expect(onClick).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("opens the confirm above near the window's bottom; no or a second click dismisses it", () => {
    const onClick = vi.fn();
    const { onClose } = renderMenu([{ label: "Delete", onClick, confirm: "Sure?" }]);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ bottom: window.innerHeight - 10 } as DOMRect);
    const del = screen.getByRole("menuitem", { name: "Delete" });
    fireEvent.click(del);
    expect(screen.getByTestId("context-menu-confirm").dataset.side).toBe("above");
    fireEvent.click(screen.getByTestId("confirm-no"));
    expect(screen.queryByTestId("context-menu-confirm")).toBeNull();
    fireEvent.click(del);
    fireEvent.click(del);
    expect(screen.queryByTestId("context-menu-confirm")).toBeNull();
    expect(onClick).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });
});
