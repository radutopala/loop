import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { RefObject } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors, lightColors } from "../../theme";
import { InfoPopup, infoBorder } from "./InfoPopup";

const rect = (top: number, height: number, right = 200) => ({ top, height, right, bottom: top + height, left: 0, width: right }) as DOMRect;

function anchorIn(sidebarRight?: number) {
  const anchor = document.createElement("div");
  if (sidebarRight !== undefined) {
    const sidebar = document.createElement("div");
    sidebar.setAttribute("data-testid", "sidebar");
    sidebar.getBoundingClientRect = () => rect(0, 800, sidebarRight);
    sidebar.append(anchor);
    document.body.append(sidebar);
  } else {
    document.body.append(anchor);
  }
  anchor.getBoundingClientRect = () => rect(100, 20, 150);
  return { current: anchor } as RefObject<HTMLElement | null>;
}

/** Gives every rendered popup the given height; anchors set their own rect. */
function popupHeight(h: number) {
  const real = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return this.dataset.testid === "popup" ? rect(0, h) : real.call(this);
  });
}

const popup = () => screen.queryByTestId("popup");

describe("InfoPopup", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("renders nothing while not hovered", () => {
    render(<InfoPopup anchorRef={anchorIn()} hovered={false} testId="popup" title="t" />);
    expect(popup()).toBeNull();
  });

  it("sits past the sidebar's edge, centered on the anchor, with the arrow at it", () => {
    popupHeight(40);
    const ref = anchorIn(300);
    render(
      <InfoPopup anchorRef={ref} hovered testId="popup" title="Full name">
        <span>label</span>
      </InfoPopup>,
    );
    const el = popup() as HTMLElement;
    expect(el.parentElement).toBe(document.body);
    expect(el.style.left).toBe("310px");
    // Anchor middle is 110; a 40px popup centers at 90.
    expect(el.style.top).toBe("90px");
    expect((el.firstChild as HTMLElement).style.top).toBe("15px");
    const title = screen.getByTestId("popup-title");
    expect(title.textContent).toBe("Full name");
    expect(title.style.paddingBottom).toBe("6px");
    expect(screen.getByText("label")).toBeTruthy();
  });

  it("sits past the anchor itself outside the sidebar, kept on screen", () => {
    popupHeight(4000);
    render(<InfoPopup anchorRef={anchorIn()} hovered testId="popup" title="t" />);
    const el = popup() as HTMLElement;
    expect(el.style.left).toBe("160px");
    expect(el.style.top).toBe("8px");
    expect(screen.getByTestId("popup-title").style.paddingBottom).toBe("");
  });

  it("keeps clear of the screen's bottom", () => {
    popupHeight(100);
    const anchor = document.createElement("div");
    document.body.append(anchor);
    anchor.getBoundingClientRect = () => rect(window.innerHeight - 10, 10);
    render(<InfoPopup anchorRef={{ current: anchor }} hovered testId="popup" title="t" />);
    expect((popup() as HTMLElement).style.top).toBe(`${window.innerHeight - 108}px`);
  });

  it("shows nothing without an anchor element", () => {
    render(<InfoPopup anchorRef={{ current: null }} hovered testId="popup" title="t" />);
    expect(popup()).toBeNull();
  });

  it.each(["mousedown", "dragstart", "wheel"])("hides on %s until hovered again", (type) => {
    const ref = anchorIn();
    const { rerender } = render(<InfoPopup anchorRef={ref} hovered testId="popup" title="t" />);
    expect(popup()).toBeTruthy();
    act(() => {
      fireEvent(window, new Event(type));
    });
    expect(popup()).toBeNull();
    rerender(<InfoPopup anchorRef={ref} hovered={false} testId="popup" title="t" />);
    rerender(<InfoPopup anchorRef={ref} hovered testId="popup" title="t" />);
    expect(popup()).toBeTruthy();
  });

  it("stops listening once unhovered", () => {
    const remove = vi.spyOn(window, "removeEventListener");
    const ref = anchorIn();
    const { rerender } = render(<InfoPopup anchorRef={ref} hovered testId="popup" title="t" />);
    rerender(<InfoPopup anchorRef={ref} hovered={false} testId="popup" title="t" />);
    expect(popup()).toBeNull();
    expect(remove.mock.calls.map((c) => c[0])).toEqual(expect.arrayContaining(["mousedown", "dragstart", "wheel"]));
  });

  it.each([
    ["dark", darkColors.inputBorder],
    ["light", lightColors.bg],
  ])("uses the %s theme's popup background", (theme, bg) => {
    render(
      <ThemeProvider initialTheme={theme}>
        <InfoPopup anchorRef={anchorIn()} hovered testId="popup" title="t" />
      </ThemeProvider>,
    );
    const probe = document.createElement("div");
    probe.style.backgroundColor = bg;
    expect((popup() as HTMLElement).style.backgroundColor).toBe(probe.style.backgroundColor);
  });
});

describe("infoBorder", () => {
  it("uses the disabled text color on dark themes and the input border on light ones", () => {
    expect(infoBorder({ isDark: true, textDisabled: "#111", inputBorder: "#222" })).toBe("1px solid #111");
    expect(infoBorder({ isDark: false, textDisabled: "#111", inputBorder: "#222" })).toBe("1px solid #222");
  });
});
