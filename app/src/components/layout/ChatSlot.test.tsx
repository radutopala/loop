import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatSlot, createChatHost } from "./ChatSlot";

/** Gives el a scroll state jsdom doesn't keep (it has no layout). */
function scrollable(el: HTMLElement, { top = 0, left = 0, height = 1000, client = 200 } = {}) {
  Object.defineProperty(el, "scrollTop", { value: top, writable: true, configurable: true });
  Object.defineProperty(el, "scrollLeft", { value: left, writable: true, configurable: true });
  Object.defineProperty(el, "scrollHeight", { value: height, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: client, configurable: true });
}

/** Places el at top..bottom, moved by shift() (a slot of another width rewraps it). */
function at(el: Element, top: number, bottom: number, shift = () => 0) {
  vi.spyOn(el, "getBoundingClientRect").mockImplementation(() => ({ top: top + shift(), bottom: bottom + shift(), left: 0, right: 0, width: 0, height: bottom - top }) as DOMRect);
}

/** How far the host's content moves once in the slot named "wide". */
function inWide(host: HTMLElement, by: number) {
  return () => (host.parentElement?.dataset.learnChatLeaf === "wide" ? by : 0);
}

function child(parent: HTMLElement, style = "") {
  const el = document.createElement("div");
  el.style.cssText = style;
  parent.appendChild(el);
  return el;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("createChatHost", () => {
  it("makes a detached element that fills its slot", () => {
    const host = createChatHost();
    expect(host.isConnected).toBe(false);
    expect(host.style.flex).toBe("1 1 0%");
    expect(host.style.display).toBe("flex");
    expect(host.style.flexDirection).toBe("column");
  });
});

describe("ChatSlot", () => {
  it("takes the host in when active and reports it, naming the leaf", () => {
    const host = createChatHost();
    const onAttach = vi.fn();
    const { container } = render(<ChatSlot host={host} active onAttach={onAttach} leafId="chat" />);
    const slot = container.firstElementChild as HTMLElement;
    expect(slot.dataset.learnChatLeaf).toBe("chat");
    expect(host.parentElement).toBe(slot);
    expect(onAttach).toHaveBeenCalledOnce();
  });

  it("leaves the host alone while inactive", () => {
    const host = createChatHost();
    const onAttach = vi.fn();
    render(<ChatSlot host={host} active={false} onAttach={onAttach} />);
    expect(host.parentElement).toBeNull();
    expect(onAttach).not.toHaveBeenCalled();
  });

  it("reports again without moving a host it already holds", () => {
    const host = createChatHost();
    const first = vi.fn();
    const second = vi.fn();
    const { container, rerender } = render(<ChatSlot host={host} active onAttach={first} />);
    const append = vi.spyOn(container.firstElementChild as HTMLElement, "appendChild");
    rerender(<ChatSlot host={host} active onAttach={second} />);
    expect(append).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledOnce();
  });

  it("parks the host off-screen, inert, as its slot unmounts, and the next slot takes it", () => {
    const host = createChatHost();
    const { container, unmount } = render(<ChatSlot host={host} active onAttach={() => {}} />);
    const slot = container.firstElementChild as HTMLElement;
    vi.spyOn(slot, "getBoundingClientRect").mockReturnValue({ width: 300, height: 400 } as DOMRect);
    unmount();
    const spot = host.parentElement!;
    expect(spot.hasAttribute("data-chat-park")).toBe(true);
    expect(spot.inert).toBe(true);
    expect(spot.getAttribute("aria-hidden")).toBe("true");
    expect(spot.style.left).toBe("-100000px");
    expect(spot.style.width).toBe("300px");
    expect(spot.style.height).toBe("400px");
    expect(spot.parentElement).toBe(document.body);

    const next = render(<ChatSlot host={host} active onAttach={() => {}} />);
    expect(host.parentElement).toBe(next.container.firstElementChild);
    expect(spot.isConnected).toBe(false);
  });

  it("doesn't park a host another slot holds", () => {
    const host = createChatHost();
    const holder = render(<ChatSlot host={host} active onAttach={() => {}} />);
    const idle = render(<ChatSlot host={host} active={false} onAttach={() => {}} />);
    const parked = document.querySelectorAll("[data-chat-park]").length;
    idle.unmount();
    expect(host.parentElement).toBe(holder.container.firstElementChild);
    expect(document.querySelectorAll("[data-chat-park]").length).toBe(parked);
  });

  it("moves a connected host with moveBefore where the slot has it", () => {
    const host = createChatHost();
    const { container, rerender } = render(
      <>
        <ChatSlot host={host} active onAttach={() => {}} />
        <ChatSlot host={host} active={false} onAttach={() => {}} />
      </>,
    );
    const [, b] = [...container.children] as HTMLElement[];
    const moveBefore = vi.fn((node: Node) => b!.appendChild(node));
    Object.assign(b!, { moveBefore });
    rerender(
      <>
        <ChatSlot host={host} active={false} onAttach={() => {}} />
        <ChatSlot host={host} active onAttach={() => {}} />
      </>,
    );
    expect(moveBefore).toHaveBeenCalledWith(host, null);
    expect(host.parentElement).toBe(b);
  });

  it("keeps a scroller near its bottom at its bottom across the move", () => {
    const host = createChatHost();
    const scroller = child(host);
    scrollable(scroller, { top: 790, height: 1000, client: 200 });
    render(<ChatSlot host={host} active onAttach={() => {}} />);
    expect(scroller.scrollTop).toBe(1000);
  });

  it("keeps a sideways scroll and a scroller with nothing in view at its offset", () => {
    const host = createChatHost();
    const sideways = child(host);
    scrollable(sideways, { left: 30, top: 0, height: 100, client: 100 });
    const scroller = child(host);
    scrollable(scroller, { top: 100 });
    render(<ChatSlot host={host} active onAttach={() => {}} />);
    expect(sideways.scrollLeft).toBe(30);
    expect(scroller.scrollTop).toBe(100);
  });

  it("keeps the element at the scroller's top edge where it was, passing stuck ones over", () => {
    const host = createChatHost();
    const scroller = child(host);
    scrollable(scroller, { top: 100 });
    at(scroller, 60, 260);
    const banner = child(scroller, "position: sticky");
    at(banner, 0, 300);
    const above = child(scroller);
    at(above, 0, 50);
    const outer = child(scroller);
    at(outer, 50, 150);
    const inner = child(outer);
    at(inner, 50, 80, inWide(host, 20));
    child(inner);
    render(<ChatSlot host={host} active onAttach={() => {}} />);
    // The anchor sat 10px above the edge, and didn't move: the scroll stays.
    expect(scroller.scrollTop).toBe(100);

    // In the wide slot the anchor rewraps 20px lower: scrolled down as much.
    const next = render(<ChatSlot host={host} active onAttach={() => {}} leafId="wide" />);
    expect(host.parentElement).toBe(next.container.firstElementChild);
    expect(scroller.scrollTop).toBe(120);
  });

  it("stops at the first child starting at or below the scroller's top edge", () => {
    const host = createChatHost();
    const scroller = child(host);
    scrollable(scroller, { top: 50 });
    at(scroller, 100, 300);
    const first = child(scroller);
    at(first, 120, 180, inWide(host, -5));
    child(first);
    render(<ChatSlot host={host} active onAttach={() => {}} leafId="wide" />);
    // Offset 20 from the edge; the anchor now sits 15 from it: scrolled 5 back.
    expect(scroller.scrollTop).toBe(45);
  });
});
