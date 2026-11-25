import { act, createEvent, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentInfo } from "../hooks/useAgentRegistry";
import { clearPaneTitle, setPaneTitle } from "../plugins/paneTitles";
import { darkColors } from "../theme";
import type { PanelType } from "../types/panels";
import { DRAG_MIME } from "./DropZoneOverlay";
import { PaneLeafHeader } from "./PaneLeafHeader";

type Props = Parameters<typeof PaneLeafHeader>[0];

function renderHeader(props: Partial<Props> = {}) {
  const { container } = render(<PaneLeafHeader leafId="leaf-1" panel="git" usedSingletons={new Set<PanelType>()} {...props} />);
  return container.firstElementChild as HTMLElement;
}

const agent = (status: string, work_summary = ""): AgentInfo => ({ agent_id: "a1", channel_id: "c1", name: "Builder", status, work_summary });

/** The color jsdom reports back for a CSS color. */
function css(color: string) {
  const el = document.createElement("span");
  el.style.color = color;
  return el.style.color;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("PaneLeafHeader label", () => {
  it.each<{ name: string; props: Partial<Props>; want: string }>([
    { name: "a panel's label", props: { panel: "git" }, want: "Git" },
    { name: "a docker agent's name", props: { panel: "docker-agent", agentInfo: agent("idle") }, want: "Builder" },
    { name: "a docker agent's leaf id without its info", props: { panel: "docker-agent" }, want: "leaf-1" },
    { name: "a plugin's label", props: { panel: "plugin", plugin: { id: "p", panel: "q", label: "Quiz" } }, want: "Quiz" },
    { name: "a chat-side pane's open mode", props: { panel: "chat-side", openMode: "fresh" }, want: "Chat · fresh" },
  ])("shows $name", ({ props, want }) => {
    const header = renderHeader(props);
    expect(header.querySelector("span")!.textContent).toBe(want);
  });

  it("shows the title a plugin panel set, for the leaf it names", () => {
    const owner = {};
    act(() => setPaneTitle("view-1", owner, "Round 3"));
    const header = renderHeader({ panel: "plugin", plugin: { id: "p", panel: "q", label: "Quiz" }, titleLeafId: "view-1" });
    expect(header.querySelector("span")!.textContent).toBe("Round 3");
    act(() => clearPaneTitle("view-1", owner));
    expect(header.querySelector("span")!.textContent).toBe("Quiz");
  });

  it.each<{ status: string; summary: string; title: string; color: string }>([
    { status: "running", summary: "building", title: "running: building", color: darkColors.active },
    { status: "error", summary: "", title: "error", color: darkColors.error },
    { status: "idle", summary: "", title: "idle", color: darkColors.textDim },
  ])("marks a $status agent", ({ status, summary, title, color }) => {
    renderHeader({ panel: "docker-agent", agentInfo: agent(status, summary) });
    const dot = screen.getByTitle(title);
    const probe = document.createElement("span");
    probe.style.backgroundColor = color;
    expect(dot.style.backgroundColor).toBe(probe.style.backgroundColor);
  });

  it.each<{ panel: PanelType; color: string }>([
    { panel: "host-shell", color: darkColors.textDim },
    { panel: "git", color: darkColors.textLight },
    { panel: "docker-agent", color: darkColors.active },
  ])("colors the $panel label", ({ panel, color }) => {
    const header = renderHeader({ panel });
    expect(header.querySelector("span")!.style.color).toBe(css(color));
  });

  it("shows the stats of the container backing the pane", () => {
    renderHeader({ panel: "chat", containerStats: { agent: { container_id: "abcdef0123456789", type: "agent", cpu_percent: 12.4, mem_usage: 1024, mem_limit: 2048 } } });
    expect(screen.getByTestId("container-stats").textContent).toContain("12%");
  });

  it("gives the header a slot for its view's labels", () => {
    const header = renderHeader();
    expect(header.querySelector("#pane-header-slot-leaf-1")).not.toBeNull();
  });
});

describe("PaneLeafHeader controls", () => {
  it("is a plain title bar without layout controls", () => {
    const header = renderHeader();
    expect(header.getAttribute("draggable")).toBe("false");
    expect(header.style.cursor).toBe("default");
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  it.each<{ name: string; props: Partial<Props>; titles: string[] }>([
    { name: "a restored pane", props: {}, titles: ["Add panel", "Minimize pane", "Expand pane", "Close pane"] },
    { name: "a maximized, minimized pane", props: { isMaximized: true, isMinimized: true }, titles: ["Add panel", "Restore pane", "Restore pane", "Close pane"] },
  ])("names its buttons for $name", ({ props, titles }) => {
    renderHeader({ onSplitLeaf: vi.fn(), onRemove: vi.fn(), onToggleMinimize: vi.fn(), onToggleMaximize: vi.fn(), ...props });
    expect(screen.getAllByRole("button").map((b) => b.title)).toEqual(titles);
  });

  it("calls each control and highlights it under the mouse", async () => {
    const onRemove = vi.fn();
    const onToggleMinimize = vi.fn();
    const onToggleMaximize = vi.fn();
    const user = userEvent.setup();
    renderHeader({ onRemove, onToggleMinimize, onToggleMaximize });
    for (const [title, fn] of [
      ["Close pane", onRemove],
      ["Minimize pane", onToggleMinimize],
      ["Expand pane", onToggleMaximize],
    ] as const) {
      const btn = screen.getByTitle(title);
      fireEvent.mouseEnter(btn);
      expect(btn.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).toBe(css(darkColors.textDim));
      expect(btn.style.backgroundColor).toBe("transparent");
      await user.click(btn);
      expect(fn).toHaveBeenCalledTimes(1);
    }
  });
});

/** A drag event on el carrying a pane drag (or other data). */
function drag(kind: "dragStart" | "dragOver" | "drop", el: Element, { types = [DRAG_MIME], data = "" }: { types?: string[]; data?: string } = {}) {
  const dataTransfer = { types, setData: vi.fn(), getData: vi.fn(() => data), effectAllowed: "", dropEffect: "none" };
  const event = createEvent[kind](el, { dataTransfer });
  fireEvent(el, event);
  return { event, dataTransfer };
}

describe("PaneLeafHeader dragging", () => {
  it("starts a pane drag with its leaf id and ends it", () => {
    const onDrop = vi.fn();
    const header = renderHeader({ onDrop });
    expect(header.getAttribute("draggable")).toBe("true");
    expect(header.style.cursor).toBe("grab");
    const started = vi.fn();
    const ended = vi.fn();
    document.addEventListener("layout-drag-start", started);
    document.addEventListener("layout-drag-end", ended);
    const { dataTransfer } = drag("dragStart", header);
    fireEvent.dragEnd(header);
    document.removeEventListener("layout-drag-start", started);
    document.removeEventListener("layout-drag-end", ended);
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_MIME, "leaf-1");
    expect(dataTransfer.effectAllowed).toBe("move");
    expect(started).toHaveBeenCalledTimes(1);
    expect(ended).toHaveBeenCalledTimes(1);
  });

  it("accepts only pane drags over the header", () => {
    const header = renderHeader({ onDrop: vi.fn() });
    const pane = drag("dragOver", header);
    expect(pane.event.defaultPrevented).toBe(true);
    expect(pane.dataTransfer.dropEffect).toBe("move");
    const other = drag("dragOver", header, { types: ["text/plain"] });
    expect(other.event.defaultPrevented).toBe(false);
    expect(other.dataTransfer.dropEffect).toBe("none");
  });

  it.each<{ name: string; data: string; called: boolean }>([
    { name: "swaps with another pane", data: "leaf-2", called: true },
    { name: "ignores itself", data: "leaf-1", called: false },
    { name: "ignores an empty drag", data: "", called: false },
  ])("a drop on the header $name", ({ data, called }) => {
    const onDrop = vi.fn();
    const header = renderHeader({ onDrop });
    const ended = vi.fn();
    document.addEventListener("layout-drag-end", ended);
    const { event } = drag("drop", header, { data });
    document.removeEventListener("layout-drag-end", ended);
    expect(event.defaultPrevented).toBe(true);
    expect(ended).toHaveBeenCalledTimes(1);
    if (called) expect(onDrop).toHaveBeenCalledWith("leaf-2", "leaf-1", "center");
    else expect(onDrop).not.toHaveBeenCalled();
  });
});

describe("PaneLeafHeader split menu", () => {
  function openMenu(props: Partial<Props> = {}, { menu = { width: 200, height: 300 }, viewport = { width: 1000, height: 800 }, anchor = { left: 100, top: 50, bottom: 70 } } = {}) {
    const onSplitLeaf = vi.fn();
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(viewport.width);
    vi.spyOn(window, "innerHeight", "get").mockReturnValue(viewport.height);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(menu.width);
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(menu.height);
    renderHeader({ onSplitLeaf, ...props });
    const btn = screen.getByTitle("Add panel");
    vi.spyOn(btn, "getBoundingClientRect").mockReturnValue({ ...anchor, right: anchor.left + 20, width: 20, height: 20 } as DOMRect);
    fireEvent.click(btn);
    return { onSplitLeaf, btn, menu: screen.getByTestId("add-panel-menu") };
  }

  it("opens below the button", () => {
    const { menu } = openMenu();
    expect(menu.style.top).toBe("72px");
    expect(menu.style.left).toBe("100px");
    expect(menu.style.maxHeight).toBe("");
    expect(menu.style.overflowY).toBe("");
  });

  it("scrolls when taller than the window", () => {
    const { menu } = openMenu({}, { menu: { width: 200, height: 2000 }, viewport: { width: 1000, height: 800 }, anchor: { left: 100, top: 50, bottom: 70 } });
    expect(menu.style.top).toBe("8px");
    expect(menu.style.maxHeight).toBe("784px");
    expect(menu.style.overflowY).toBe("auto");
  });

  it.each([
    { label: "Notes ↓", direction: "vertical", meta: undefined, panel: "notes" },
    { label: "Notes →", direction: "horizontal", meta: undefined, panel: "notes" },
    { label: "Chat Terminal (Fresh session) →", direction: "horizontal", meta: { openMode: "fresh" }, panel: "docker-agent" },
  ])("splits the pane with $label", ({ label, direction, meta, panel }) => {
    const { onSplitLeaf, menu } = openMenu();
    const item = within(menu).getByRole("button", { name: label });
    fireEvent.mouseEnter(item);
    expect(item.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(item);
    expect(item.style.backgroundColor).toBe("transparent");
    fireEvent.click(item);
    expect(onSplitLeaf).toHaveBeenCalledWith("leaf-1", panel, direction, meta);
    expect(screen.queryByTestId("add-panel-menu")).toBeNull();
  });

  it("disables singletons the layout shows and drops hidden panels", () => {
    const { menu, onSplitLeaf } = openMenu({ usedSingletons: new Set<PanelType>(["chat"]), hiddenPanels: ["notes"] });
    const [down, right] = within(menu).getAllByRole("button", { name: /^Chat [↓→]$/ }) as HTMLButtonElement[];
    expect(down!.disabled).toBe(true);
    expect(right!.disabled).toBe(true);
    expect(down!.style.opacity).toBe("0.35");
    expect(down!.style.cursor).toBe("default");
    const before = down!.style.backgroundColor;
    fireEvent.mouseEnter(down!);
    expect(down!.style.backgroundColor).toBe(before);
    fireEvent.click(right!);
    expect(onSplitLeaf).not.toHaveBeenCalled();
    expect(within(menu).queryByRole("button", { name: /^Notes/ })).toBeNull();
  });

  it("closes on a click outside, the button toggling it", () => {
    const { btn, menu } = openMenu();
    fireEvent.mouseDown(menu.firstElementChild!);
    fireEvent.mouseDown(btn);
    expect(screen.getByTestId("add-panel-menu")).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId("add-panel-menu")).toBeNull();
    fireEvent.click(btn);
    expect(screen.getByTestId("add-panel-menu")).toBeTruthy();
    fireEvent.click(btn);
    expect(screen.queryByTestId("add-panel-menu")).toBeNull();
  });

  it("highlights the add button under the mouse", () => {
    renderHeader({ onSplitLeaf: vi.fn() });
    const btn = screen.getByTitle("Add panel");
    fireEvent.mouseEnter(btn);
    expect(btn.style.color).toBe(css(darkColors.textLight));
    fireEvent.mouseLeave(btn);
    expect(btn.style.color).toBe(css(darkColors.textDim));
  });
});
