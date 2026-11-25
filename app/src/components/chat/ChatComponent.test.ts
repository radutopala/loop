// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatComponent, ChatComponentFull, ComponentFocusContext, clickedInFrame, type ShownComponent } from "./ChatComponent";

describe("clickedInFrame", () => {
  // Identity is all clickedInFrame compares.
  const frame = {} as HTMLIFrameElement;
  const other = {} as Element;
  const cases = [
    { name: "a live click in the frame", active: frame, activation: { isActive: true }, want: true },
    { name: "a live click elsewhere in the app", active: other, activation: { isActive: true }, want: false },
    { name: "focus in the frame, but no live click", active: frame, activation: { isActive: false }, want: false },
    { name: "no userActivation API", active: frame, activation: undefined, want: false },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(clickedInFrame(frame, { activeElement: c.active }, c.activation)).toBe(c.want);
    });
  }
});

const shown: ShownComponent = { template: "chart", title: "Weekly runs", doc: "<p>hi</p>" };

// Posts data to the window as if from source, as a frame's document does.
function post(source: Window | null, data: unknown) {
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { data, source }));
  });
}

function setActivation(isActive: boolean) {
  Object.defineProperty(navigator, "userActivation", { configurable: true, value: { isActive } });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  delete (navigator as { userActivation?: unknown }).userActivation;
});

describe("ChatComponent", () => {
  it("renders the document in a sandboxed frame under its title", () => {
    render(createElement(ChatComponent, shown));
    const frame = screen.getByTestId("chat-component-frame") as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
    expect(frame.getAttribute("srcdoc")).toBe("<p>hi</p>");
    expect(frame.getAttribute("title")).toBe("Weekly runs");
    expect(screen.getByTestId("chat-component-title").textContent).toBe("Weekly runs");
    expect(screen.getByTestId("chat-component").getAttribute("data-template")).toBe("chart");
    expect(frame.style.height).toBe("160px");
  });

  it("falls back to the template's name without a title", () => {
    render(createElement(ChatComponent, { ...shown, title: "" }));
    expect(screen.getByTestId("chat-component-title").textContent).toBe("chart");
  });

  it("follows the height its document posts, ignoring other windows and messages", () => {
    render(createElement(ChatComponent, shown));
    const frame = screen.getByTestId("chat-component-frame") as HTMLIFrameElement;
    post(frame.contentWindow, { type: "loop-component-height", height: 300.2 });
    expect(frame.style.height).toBe("301px");
    post(window, { type: "loop-component-height", height: 500 });
    post(null, { type: "loop-component-height", height: 500 });
    post(frame.contentWindow, { type: "other" });
    expect(frame.style.height).toBe("301px");
  });

  it("opens a link clicked in its document in the browser", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    render(createElement(ChatComponent, shown));
    const frame = screen.getByTestId("chat-component-frame") as HTMLIFrameElement;
    frame.focus();
    setActivation(true);
    post(frame.contentWindow, { type: "loop-component-open", url: "https://example.com/a" });
    expect(open).toHaveBeenCalledWith("https://example.com/a", "_blank", "noopener,noreferrer");
  });

  it("doesn't open a link its document posts on its own", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    render(createElement(ChatComponent, shown));
    const frame = screen.getByTestId("chat-component-frame") as HTMLIFrameElement;
    setActivation(false);
    post(frame.contentWindow, { type: "loop-component-open", url: "https://example.com/a" });
    expect(open).not.toHaveBeenCalled();
    expect(frame.style.height).toBe("160px");
  });

  it("has no expand button without a pane to open it in", () => {
    render(createElement(ChatComponent, shown));
    expect(screen.queryByTestId("chat-component-expand")).toBeNull();
  });

  it("opens across the pane, its actions showing on hover", () => {
    const openFull = vi.fn();
    render(createElement(ComponentFocusContext.Provider, { value: openFull }, createElement(ChatComponent, shown)));
    const expand = screen.getByTestId("chat-component-expand");
    expect(expand.style.opacity).toBe("0");
    fireEvent.mouseEnter(screen.getByTestId("chat-component"));
    expect(expand.style.opacity).toBe("1");
    fireEvent.click(expand);
    expect(openFull).toHaveBeenCalledWith(shown);
    fireEvent.mouseLeave(screen.getByTestId("chat-component"));
    expect(expand.style.opacity).toBe("0");
  });

  it("stops listening for its document's messages on unmount", () => {
    const remove = vi.spyOn(window, "removeEventListener");
    const { unmount } = render(createElement(ChatComponent, shown));
    unmount();
    expect(remove).toHaveBeenCalledWith("message", expect.any(Function));
  });
});

describe("ChatComponentFull", () => {
  it("shows the document as a dialog that closes on Escape or the close button", () => {
    const onClose = vi.fn();
    render(createElement(ChatComponentFull, { component: { ...shown, title: "" }, onClose }));
    const dialog = screen.getByRole("dialog", { name: "chart" });
    expect(dialog.getAttribute("data-template")).toBe("chart");
    fireEvent.keyDown(window, { key: "a" });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByTestId("chat-component-close"));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("ignores its document's height and stops listening on unmount", () => {
    const onClose = vi.fn();
    const { unmount } = render(createElement(ChatComponentFull, { component: shown, onClose }));
    const frame = screen.getByTitle("Weekly runs") as HTMLIFrameElement;
    post(frame.contentWindow, { type: "loop-component-height", height: 300 });
    expect(frame.style.height).toBe("");
    unmount();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
  });
});
