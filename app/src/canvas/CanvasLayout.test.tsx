import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PluginInfo } from "../api/plugins";
import { PluginPanelsProvider } from "../plugins/PluginPanelsContext";
import { ThemeProvider } from "../ThemeContext";
import type { LeafNode, PanelType } from "../types/panels";
import { type CanvasControl, CanvasLayout, type LearnDock } from "./CanvasLayout";
import type { CanvasNode, CanvasTile } from "./types";

// The animated logo on the Learn dock draws with SVG path measuring jsdom lacks.
vi.mock("../components/LoopInfinityIcon", () => ({ LoopInfinityIcon: () => <span /> }));

const RECT = { left: 10, top: 20, width: 1000, height: 800 };

function tile(id: string, panel: PanelType, x: number, y: number, width = 500, height = 400, zIndex = 1): CanvasTile {
  return { id, panel, x, y, width, height, zIndex };
}

function canvasOf(tiles: CanvasTile[], viewport = { x: 5, y: 5, zoom: 1 }): CanvasNode {
  return { type: "canvas", tiles, viewport };
}

interface HarnessProps {
  initial: CanvasNode;
  onChange: (c: CanvasNode) => void;
  learnDock?: LearnDock;
  controlRef?: React.MutableRefObject<CanvasControl | null>;
  hiddenPanels?: PanelType[];
}

/** Holds the canvas the way the app does, reporting each change. */
function Harness({ initial, onChange, ...rest }: HarnessProps) {
  const [canvas, setCanvas] = useState(initial);
  return (
    <CanvasLayout
      canvas={canvas}
      renderLeaf={(leaf: LeafNode) => <div>leaf {leaf.id}</div>}
      onCanvasChange={(c) => {
        onChange(c);
        setCanvas(c);
      }}
      {...rest}
    />
  );
}

function renderLayout(initial: CanvasNode, rest: Omit<HarnessProps, "initial" | "onChange"> = {}, opts: { plugins?: PluginInfo[]; theme?: string } = {}) {
  const onChange = vi.fn<(c: CanvasNode) => void>();
  const tree = () => (
    <ThemeProvider initialTheme={opts.theme}>
      <PluginPanelsProvider value={{ plugins: opts.plugins ?? [], shown: new Set() }}>
        <Harness initial={initial} onChange={onChange} {...rest} />
      </PluginPanelsProvider>
    </ThemeProvider>
  );
  const view = render(tree());
  const last = () => onChange.mock.calls.at(-1)?.[0] as CanvasNode;
  return { onChange, last, rerender: () => view.rerender(tree()), unmount: view.unmount };
}

/** The canvas's own element, found from the first tile's leaf. */
function canvasRoot(): HTMLElement {
  return (screen.getAllByText(/^leaf /)[0] as HTMLElement).closest("[data-canvas-tile]")?.parentElement?.parentElement as HTMLElement;
}

/** The tile header holding label. */
function header(label: string): HTMLElement {
  return screen.getByText(label).parentElement as HTMLElement;
}

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    ...RECT,
    right: RECT.left + RECT.width,
    bottom: RECT.top + RECT.height,
    x: RECT.left,
    y: RECT.top,
    toJSON: () => ({}),
  } as DOMRect);
  vi.spyOn(Date, "now").mockReturnValue(1000);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CanvasLayout", () => {
  it("fits the tiles in view at the default viewport", () => {
    const { last } = renderLayout(canvasOf([tile("a", "notes", 0, 0), tile("b", "git", 600, 0)], { x: 0, y: 0, zoom: 1 }));
    const zoom = 900 / 1100;
    expect(last().viewport.zoom).toBeCloseTo(zoom);
    expect(last().viewport.x).toBeCloseTo(50);
    expect(last().viewport.y).toBeCloseTo((800 - 400 * zoom) / 2);
  });

  it("leaves a viewport that was moved", () => {
    const { onChange } = renderLayout(canvasOf([tile("a", "notes", 0, 0)], { x: 0, y: 0, zoom: 0.5 }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("50%")).toBeTruthy();
  });

  it("offers the panels on an empty canvas, adding one at the view's center", () => {
    const { last } = renderLayout(canvasOf([], { x: 0, y: 0, zoom: 1 }), { hiddenPanels: ["memory"] });
    expect(screen.queryByTestId("empty-layout-add-memory")).toBeNull();
    fireEvent.click(screen.getByTestId("empty-layout-add-notes"));
    expect(last().tiles).toEqual([{ id: "notes-1000", panel: "notes", x: 250, y: 200, width: 500, height: 400, zIndex: 1 }]);
    expect(screen.queryByTestId("empty-layout-add-notes")).toBeNull();
  });

  it("keeps an agent terminal's open mode, and gives an editor a file tree", () => {
    const { last } = renderLayout(canvasOf([]));
    fireEvent.click(screen.getByTestId("empty-layout-add-docker-agent-resume"));
    expect(last().tiles[0]).toMatchObject({ panel: "docker-agent", openMode: "resume" });
  });

  it("adds an editor with a file tree left of it", () => {
    const { last } = renderLayout(canvasOf([]));
    fireEvent.click(screen.getByTestId("empty-layout-add-editor"));
    const [editor, tree] = last().tiles as [CanvasTile, CanvasTile];
    expect(editor).toMatchObject({ id: "editor-1000", panel: "editor", width: 900, height: 900, zIndex: 1 });
    expect(tree).toMatchObject({ id: "file-tree-1000", panel: "file-tree", x: editor.x - 520, y: editor.y, width: 500, height: 400, zIndex: 2 });
  });

  it("adds a plugin's panel with its ref", () => {
    const plugins: PluginInfo[] = [{ id: "p", name: "Pl", version: "1", capabilities: [], status: "running", panels: [{ id: "board", label: "Board", entry: "index.html" }] }];
    const { last } = renderLayout(canvasOf([]), {}, { plugins });
    fireEvent.click(screen.getByTestId("empty-layout-add-plugin-p/board"));
    expect(last().tiles[0]).toMatchObject({ panel: "plugin", plugin: { id: "p", panel: "board", label: "Board" } });
  });

  it("opens the add menu on a background double click, adding the tile there", () => {
    const { last } = renderLayout(canvasOf([tile("chat", "chat", 0, 0), tile("e", "editor", 600, 0, 500, 400, 2), tile("b", "docker-browser", 0, 2000)]));
    fireEvent.doubleClick(screen.getByText("leaf chat"));
    expect(screen.queryByRole("button", { name: "Notes" })).toBeNull();

    const canvasEl = canvasRoot();
    fireEvent.doubleClick(canvasEl, { clientX: 110, clientY: 2620 });
    const chat = screen.getAllByRole("button", { name: "Chat" }).at(-1) as HTMLButtonElement;
    expect(chat.disabled).toBe(true);
    expect((screen.getAllByRole("button", { name: "Host Browser" }).at(-1) as HTMLButtonElement).disabled).toBe(true);
    const notes = screen.getAllByRole("button", { name: "Notes" }).at(-1) as HTMLElement;
    fireEvent.mouseEnter(notes);
    expect(notes.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(notes);
    expect(notes.style.backgroundColor).toBe("transparent");
    fireEvent.click(notes);
    // The menu's spot, (100, 2600) in the view, in world units past the viewport's (5, 5).
    expect(last().tiles.at(-1)).toEqual({ id: "notes-1000", panel: "notes", x: 95, y: 2595, width: 500, height: 400, zIndex: 3 });
  });

  it("adds an editor from the menu without a second file tree, and a side chat with its mode", () => {
    const { last } = renderLayout(canvasOf([tile("t", "file-tree", 0, 0)]));
    const canvasEl = canvasRoot();
    fireEvent.doubleClick(canvasEl, { clientX: 1010, clientY: 20 });
    fireEvent.click(screen.getAllByRole("button", { name: "Editor" }).at(-1) as HTMLElement);
    expect(last().tiles.map((t) => t.panel)).toEqual(["file-tree", "editor"]);

    fireEvent.doubleClick(canvasEl, { clientX: 1010, clientY: 20 });
    fireEvent.click(screen.getAllByRole("button", { name: "Chat (Fresh session)" }).at(-1) as HTMLElement);
    expect(last().tiles.at(-1)).toMatchObject({ panel: "chat-side", openMode: "fresh" });
  });

  it("closes the add menu on a click elsewhere on the canvas, not in it", () => {
    renderLayout(canvasOf([tile("a", "notes", 0, 0)]));
    const canvasEl = canvasRoot();
    fireEvent.doubleClick(canvasEl, { clientX: 900, clientY: 700 });
    const menu = (screen.getAllByRole("button", { name: "Git" }).at(-1) as HTMLElement).parentElement as HTMLElement;
    fireEvent.click(menu);
    expect(menu.isConnected).toBe(true);
    fireEvent.click(canvasEl);
    expect(menu.isConnected).toBe(false);
  });

  it("adds a tile from a tile's add menu right of that tile", () => {
    const { last } = renderLayout(canvasOf([tile("a", "notes", 0, 0)]));
    fireEvent.click(screen.getByTitle("Add panel"));
    fireEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(last().tiles.at(-1)).toMatchObject({ panel: "git", x: 520, y: 0, zIndex: 2 });
  });

  it("moves, resizes, raises and closes tiles", () => {
    const { last, onChange } = renderLayout(canvasOf([tile("a", "notes", 0, 0, 500, 400, 1), tile("b", "git", 600, 0, 500, 400, 5)]));
    fireEvent.mouseDown(header("Notes"), { clientX: 0, clientY: 0 });
    expect(last().tiles[0]?.zIndex).toBe(6);
    fireEvent.mouseMove(document, { clientX: 30, clientY: 40 });
    fireEvent.mouseUp(document);
    expect(last().tiles[0]).toMatchObject({ x: 30, y: 40 });

    const corner = header("Git").parentElement?.lastElementChild as HTMLElement;
    fireEvent.mouseDown(corner, { clientX: 0, clientY: 0 });
    fireEvent.mouseMove(document, { clientX: 100, clientY: 50 });
    fireEvent.mouseUp(document);
    expect(last().tiles[1]).toMatchObject({ width: 600, height: 450, zIndex: 7 });

    // The top tile stays as it is.
    onChange.mockClear();
    fireEvent.mouseDown(screen.getByText("leaf b"));
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getAllByTitle("Close tile")[0] as HTMLElement);
    expect(last().tiles.map((t) => t.id)).toEqual(["b"]);
  });

  it("maximizes a tile to fill the view and restores it", () => {
    const { last } = renderLayout(canvasOf([tile("a", "notes", 0, 0, 500, 400, 1), tile("b", "git", 600, 0, 500, 400, 3)], { x: -100, y: -50, zoom: 2 }));
    fireEvent.click(screen.getAllByTitle("Maximize tile")[0] as HTMLElement);
    expect(last().tiles[0]).toEqual({ id: "a", panel: "notes", x: 55, y: 30, width: 490, height: 390, zIndex: 4 });
    fireEvent.click(screen.getByTitle("Restore tile"));
    expect(last().tiles[0]).toEqual({ id: "a", panel: "notes", x: 0, y: 0, width: 500, height: 400, zIndex: 4 });
    expect(screen.queryByTitle("Restore tile")).toBeNull();
  });

  it("lets UI steps maximize tiles through its control", () => {
    const controlRef: React.MutableRefObject<CanvasControl | null> = { current: null };
    const c = canvasOf([tile("a", "notes", 0, 0), tile("b", "git", 600, 0)], { x: 0, y: 0, zoom: 0.5 });
    renderLayout(c, { controlRef });
    const control = controlRef.current as CanvasControl;
    expect(control.maximized()).toBeNull();
    expect(control.maximize(c, null)).toBe(c);
    expect(control.maximize(c, "missing")).toBe(c);
    expect(control.maximized()).toBeNull();

    let a = c;
    act(() => {
      a = control.maximize(c, "a");
    });
    expect(control.maximized()).toBe("a");
    expect(a.tiles[0]).toMatchObject({ x: 20, y: 20, width: 1960, height: 1560, zIndex: 2 });
    expect(control.maximize(a, "a")).toBe(a);

    let b = a;
    act(() => {
      b = control.maximize(a, "b");
    });
    expect(control.maximized()).toBe("b");
    expect(b.tiles[0]).toMatchObject({ x: 0, y: 0, width: 500, height: 400 });
    expect(b.tiles[1]).toMatchObject({ x: 20, y: 20, width: 1960, height: 1560 });

    let none = b;
    act(() => {
      none = control.maximize(b, null);
    });
    expect(control.maximized()).toBeNull();
    expect(none.tiles[1]).toMatchObject({ x: 600, y: 0, width: 500, height: 400 });
  });

  it("maximizes to a default view size once the canvas is gone", () => {
    const controlRef: React.MutableRefObject<CanvasControl | null> = { current: null };
    const c = canvasOf([tile("a", "notes", 0, 0)], { x: 0, y: 0, zoom: 2 });
    const { unmount } = renderLayout(c, { controlRef });
    unmount();
    const got = (controlRef.current as CanvasControl).maximize(c, "a");
    expect(got.tiles[0]).toMatchObject({ x: 5, y: 5, width: 590, height: 390 });
  });

  it("keeps the minimap's scale finite for a canvas with no extent", () => {
    vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockReturnValue(new DOMRect(0, 0, 0, 0));
    const { rerender } = renderLayout(canvasOf([tile("a", "notes", 0, 0, 0, 0)], { x: 0, y: 0, zoom: 2 }));
    rerender();
    const map = screen.getByText("200%").parentElement?.previousElementSibling as HTMLElement;
    // A 1-by-1 world fits the 160-by-100 minimap at 100 per unit.
    expect(map.style.width).toBe("100px");
    expect(map.style.height).toBe("100px");
  });

  it("pans on a middle-button drag only", () => {
    const { last, onChange } = renderLayout(canvasOf([tile("a", "notes", 0, 0)], { x: 10, y: 10, zoom: 1 }));
    const canvasEl = canvasRoot();
    fireEvent.mouseDown(canvasEl, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(document, { clientX: 50, clientY: 50 });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.mouseUp(document);

    fireEvent.mouseDown(canvasEl, { button: 1, clientX: 100, clientY: 100 });
    fireEvent.mouseMove(document, { clientX: 130, clientY: 80 });
    expect(last().viewport).toEqual({ x: 40, y: -10, zoom: 1 });
    fireEvent.mouseUp(document);
    onChange.mockClear();
    fireEvent.mouseMove(document, { clientX: 500, clientY: 500 });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("zooms about the cursor on a ctrl or cmd wheel, within bounds, and pans on a plain wheel off the tiles", () => {
    const { last, onChange } = renderLayout(canvasOf([tile("a", "notes", 0, 0)], { x: 0, y: 0, zoom: 0.5 }));
    const canvasEl = canvasRoot();
    fireEvent.wheel(canvasEl, { ctrlKey: true, deltaY: -100, clientX: 110, clientY: 120 });
    expect(last().viewport.zoom).toBeCloseTo(0.6);
    expect(last().viewport.x).toBeCloseTo(100 - 100 * 1.2);
    expect(last().viewport.y).toBeCloseTo(100 - 100 * 1.2);

    fireEvent.wheel(canvasEl, { metaKey: true, deltaY: 100000, clientX: 10, clientY: 20 });
    expect(last().viewport.zoom).toBe(0.25);
    fireEvent.wheel(canvasEl, { ctrlKey: true, deltaY: -100000, clientX: 10, clientY: 20 });
    expect(last().viewport.zoom).toBe(2);

    const before = last().viewport;
    fireEvent.wheel(canvasEl, { deltaX: 5, deltaY: 7 });
    expect(last().viewport).toEqual({ x: before.x - 5, y: before.y - 7, zoom: 2 });

    onChange.mockClear();
    fireEvent.wheel(screen.getByText("leaf a"), { deltaX: 5, deltaY: 7 });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("shows the minimap's zoom and zooms about the view's center", () => {
    const { last } = renderLayout(canvasOf([tile("a", "notes", 0, 0)], { x: 0, y: 0, zoom: 0.5 }));
    expect(screen.getByText("50%")).toBeTruthy();
    fireEvent.click(screen.getByTitle("Zoom in"));
    expect(last().viewport.zoom).toBeCloseTo(0.6);
    expect(last().viewport.x).toBeCloseTo(500 - 500 * 1.2);
    expect(last().viewport.y).toBeCloseTo(400 - 400 * 1.2);
    fireEvent.click(screen.getByTitle("Reset zoom"));
    expect(last().viewport.zoom).toBe(1);
    fireEvent.click(screen.getByTitle("Zoom out"));
    expect(last().viewport.zoom).toBeCloseTo(1 / 1.2);
    for (const title of ["Zoom in", "Zoom out"]) {
      const btn = screen.getByTitle(title);
      const resting = btn.style.color;
      fireEvent.mouseEnter(btn);
      expect(btn.style.color).not.toBe(resting);
      fireEvent.mouseLeave(btn);
      expect(btn.style.color).toBe(resting);
    }
  });

  it("clamps the minimap's zoom buttons", () => {
    const { last } = renderLayout(canvasOf([tile("a", "notes", 0, 0)], { x: 0, y: 0, zoom: 1.9 }));
    fireEvent.click(screen.getByTitle("Zoom in"));
    expect(last().viewport.zoom).toBe(2);
    const { last: last2 } = renderLayout(canvasOf([tile("b", "git", 0, 0)], { x: 0, y: 0, zoom: 0.26 }));
    fireEvent.click(screen.getAllByTitle("Zoom out")[1] as HTMLElement);
    expect(last2().viewport.zoom).toBe(0.25);
  });

  // One tile covering the view's world box, (0,0)-(2000,1600) at zoom 0.5,
  // so the minimap scales by 0.0625.
  const fullView = () => canvasOf([tile("a", "notes", 0, 0, 2000, 1600)], { x: 0, y: 0, zoom: 0.5 });
  const minimap = () => screen.getByText("50%").parentElement?.previousElementSibling as HTMLElement;

  it("jumps the view to a minimap click", () => {
    const { last, rerender } = renderLayout(fullView());
    // The minimap reads the canvas's size from its second render on.
    rerender();
    // Clicks land relative to the minimap's rect, which the stub puts at (10, 20).
    fireEvent.click(minimap(), { clientX: 10 + 25, clientY: 20 + 50 });
    // (25, 50) on the minimap is (400, 800) in the world, the view's new center.
    expect(last().viewport).toEqual({ x: 300, y: -0, zoom: 0.5 });
  });

  it("pans with the minimap's viewport box", () => {
    const { last, onChange } = renderLayout(fullView());
    const vpBox = minimap().lastElementChild as HTMLElement;
    fireEvent.click(vpBox);
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.mouseDown(vpBox, { clientX: 0, clientY: 0 });
    fireEvent.mouseMove(document, { clientX: 10, clientY: 5 });
    expect(last().viewport).toEqual({ x: -80, y: -40, zoom: 0.5 });
    fireEvent.mouseUp(document);
    onChange.mockClear();
    fireEvent.mouseMove(document, { clientX: 50, clientY: 50 });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("colors minimap tiles by panel, a panel it doesn't know dimly, on a light theme", () => {
    renderLayout(canvasOf([tile("a", "notes", 0, 0), tile("x", "retired" as PanelType, 600, 0)]), {}, { theme: "light" });
    const box = screen.getByText("100%").parentElement?.previousElementSibling as HTMLElement;
    const [notes, retired] = Array.from(box.children) as [HTMLElement, HTMLElement];
    expect(notes.style.backgroundColor).toBe("rgb(249, 168, 212)");
    expect(retired.style.backgroundColor).not.toBe("");
    expect(box.style.backgroundColor).toBe("rgba(255, 255, 255, 0.8)");
  });

  describe("learn dock", () => {
    const dock = (over: Partial<LearnDock> = {}): LearnDock => ({ shown: true, ms: 0, running: false, pane: <div>learn pane</div>, ...over });

    it("docks to the chat's tile, moving and resizing it", () => {
      const { last } = renderLayout(canvasOf([tile("chat", "chat", 0, 0, 300, 200)]), { learnDock: dock() });
      const learn = screen.getByTestId("canvas-learn-dock");
      expect(learn.style.left).toBe("324px");
      fireEvent.mouseDown(learn.lastElementChild as HTMLElement, { clientX: 0, clientY: 0 });
      fireEvent.mouseMove(document, { clientX: 100, clientY: 10 });
      fireEvent.mouseUp(document);
      expect(last().tiles[0]).toMatchObject({ width: 350, height: 210 });
    });

    it("docks to the tile it names", () => {
      renderLayout(canvasOf([tile("chat", "chat", 0, 0, 300, 200), tile("cs", "chat-side", 0, 300, 100, 100)]), { learnDock: dock({ tileId: "cs", view: "explain" }) });
      const learn = screen.getByTestId("canvas-learn-dock");
      expect(learn.style.left).toBe("124px");
      expect(learn.dataset.view).toBe("explain");
    });

    it("isn't shown without its tile", () => {
      const { onChange } = renderLayout(canvasOf([tile("n", "notes", 0, 0)]), { learnDock: dock() });
      expect(screen.queryByTestId("canvas-learn-dock")).toBeNull();
      expect(onChange).not.toHaveBeenCalled();
    });

    it("brings the pair into view, zooming out when they don't fit", () => {
      const { last } = renderLayout(canvasOf([tile("chat", "chat", 2000, 100, 800, 600)], { x: 5, y: 5, zoom: 1 }), { learnDock: dock() });
      const w = 800 * 2 + 24;
      const zoom = (1000 * 0.95) / w;
      expect(last().viewport.zoom).toBeCloseTo(zoom);
      expect(last().viewport.x).toBeCloseTo((1000 - w * zoom) / 2 - 2000 * zoom);
      expect(last().viewport.y).toBeCloseTo((800 - 600 * zoom) / 2 - 100 * zoom);
    });

    it("zooms out no further than the minimum", () => {
      const { last } = renderLayout(canvasOf([tile("chat", "chat", 0, 0, 5000, 5000)], { x: 5, y: 5, zoom: 1 }), { learnDock: dock() });
      expect(last().viewport.zoom).toBe(0.25);
    });

    it.each([
      { name: "left", x: -100, y: 10 },
      { name: "above", x: 10, y: -100 },
      { name: "below", x: 10, y: 700 },
    ])("pans to the pair when it's off the view's $name edge", ({ x, y }) => {
      const { last } = renderLayout(canvasOf([tile("chat", "chat", x, y, 200, 200)], { x: 5, y: 5, zoom: 1 }), { learnDock: dock() });
      expect(last().viewport.zoom).toBe(1);
      expect(last().viewport.x).toBeCloseTo((1000 - 424) / 2 - x);
    });

    it("leaves a view the pair fits in", () => {
      const { onChange } = renderLayout(canvasOf([tile("chat", "chat", 10, 10, 200, 200)], { x: 5, y: 5, zoom: 1 }), { learnDock: dock() });
      expect(onChange).not.toHaveBeenCalled();
    });
  });
});
