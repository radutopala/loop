import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkflowNodeDef, WorkflowNodeRun } from "../../api/workflows";
import { darkColors as colors } from "../../theme";
import { WorkflowGraph } from "./WorkflowGraph";

function def(id: string, type: WorkflowNodeDef["type"], extra: Partial<WorkflowNodeDef> = {}): WorkflowNodeDef {
  return { id, type, ...extra };
}

function run(nodeId: string, extra: Partial<WorkflowNodeRun> = {}): WorkflowNodeRun {
  return {
    id: 0,
    run_id: "r",
    node_id: nodeId,
    iteration: 0,
    status: "success",
    input: "",
    session_id: "",
    output: "",
    error_text: "",
    attempt: 1,
    started_at: null,
    finished_at: null,
    last_heartbeat_at: null,
    ...extra,
  };
}

// Normalizes a hex color the way jsdom stores it in an HTML style.
function cssColor(hex: string): string {
  const probe = document.createElement("div");
  probe.style.color = hex;
  return probe.style.color;
}

function nodeEl(container: HTMLElement, id: string): SVGGElement {
  const el = container.querySelector<SVGGElement>(`g[data-node="${id}"]`);
  if (!el) throw new Error(`node ${id} not rendered`);
  return el;
}

function nodeRect(container: HTMLElement, id: string): SVGRectElement {
  return nodeEl(container, id).querySelector<SVGRectElement>('rect[width="140"]')!;
}

function transformLayer(container: HTMLElement): HTMLElement {
  return container.querySelector("svg")!.parentElement!;
}

function canvas(container: HTMLElement): HTMLElement {
  return transformLayer(container).parentElement!;
}

function viewport(container: HTMLElement): { x: number; y: number; zoom: number } {
  const m = /translate\(([-\d.e]+)px, ([-\d.e]+)px\) scale\(([-\d.e]+)\)/.exec(transformLayer(container).style.transform);
  if (!m) throw new Error(`unexpected transform ${transformLayer(container).style.transform}`);
  return { x: Number(m[1]), y: Number(m[2]), zoom: Number(m[3]) };
}

function minimap(): HTMLElement {
  return screen.getByTitle("Zoom out").parentElement!.previousElementSibling as HTMLElement;
}

function edgePaths(container: HTMLElement): SVGPathElement[] {
  return Array.from(container.querySelectorAll<SVGPathElement>("svg > path"));
}

describe("WorkflowGraph", () => {
  beforeEach(() => {
    // jsdom has no layout; give every element a 600x400 box at the origin.
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      x: 0,
      y: 0,
      left: 0,
      top: 0,
      right: 600,
      bottom: 400,
      width: 600,
      height: 400,
      toJSON: () => ({}),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("shows a placeholder when there are no defs and no runs", () => {
    render(<WorkflowGraph defs={[]} nodeRuns={[]} colors={colors} />);
    expect(screen.getByText("No nodes defined")).toBeTruthy();
  });

  it("builds one node per distinct node_id from runs when defs are missing", () => {
    const { container } = render(<WorkflowGraph defs={[]} nodeRuns={[run("x"), run("x", { iteration: 1 }), run("y")]} colors={colors} />);
    const ids = Array.from(container.querySelectorAll("g[data-node]")).map((g) => g.getAttribute("data-node"));
    expect(ids).toEqual(["x", "y"]);
    // Fallback defs are bash nodes.
    expect(nodeEl(container, "x").textContent).toContain("B");
  });

  it("centers and fits the graph once on mount", () => {
    const { container, rerender } = render(<WorkflowGraph defs={[def("a", "bash")]} nodeRuns={[]} colors={colors} />);
    // One node: layout 220x124. Fit zoom is capped at 1.2.
    const vp = viewport(container);
    expect(vp.zoom).toBeCloseTo(1.2);
    expect(vp.x).toBeCloseTo((600 - 220 * 1.2) / 2);
    expect(vp.y).toBeCloseTo((400 - 124 * 1.2) / 2);
    expect(screen.getByText("120%")).toBeTruthy();

    // A later layout change keeps the viewport where it is.
    fireEvent.click(screen.getByTitle("Zoom in"));
    const zoomed = viewport(container);
    rerender(<WorkflowGraph defs={[def("a", "bash"), def("b", "bash", { depends_on: ["a"] })]} nodeRuns={[]} colors={colors} />);
    expect(viewport(container)).toEqual(zoomed);
  });

  it("lays out deps left to right and draws edges styled by the target's status", () => {
    const defs = [
      def("root", "bash"),
      def("ok", "prompt", { depends_on: ["root"] }),
      def("wait", "approval", { depends_on: ["root"] }),
      def("skip", "plugin", { depends_on: ["root"] }),
      def("none", "bash", { depends_on: ["root"] }),
      def("odd", "bash", { depends_on: ["root"] }),
      // Shares "ok" and "wait" as parents, so their layers come from the cache.
      def("join", "bash", { depends_on: ["ok", "wait"] }),
    ];
    const runs = [
      run("ok", { status: "running" }),
      run("wait", { status: "pending" }),
      run("skip", { status: "skipped" }),
      run("odd", { status: "mystery" as WorkflowNodeRun["status"] }),
      run("join", { status: "failed" }),
    ];
    const { container } = render(<WorkflowGraph defs={defs} nodeRuns={runs} colors={colors} />);

    const x = (id: string) => Number(nodeRect(container, id).getAttribute("x"));
    expect(x("root")).toBe(40);
    expect(x("ok")).toBe(240);
    expect(x("join")).toBe(440);

    const paths = edgePaths(container);
    // root→ok, root→wait, root→skip, root→none, root→odd, ok→join, wait→join
    expect(paths).toHaveLength(7);
    const [toOk, toWait, toSkip, toNone, toOdd, toJoin] = paths;
    expect(toOk!.getAttribute("stroke")).toBe("#818cf8");
    expect(toOk!.getAttribute("stroke-width")).toBe("2");
    expect(toOk!.getAttribute("stroke-dasharray")).toBeNull();
    expect(toOk!.getAttribute("marker-end")).toBe("url(#wf-arrow-running)");
    expect(toOk!.getAttribute("opacity")).toBe("1");

    expect(toWait!.getAttribute("stroke-dasharray")).toBe("4 3");
    expect(toWait!.getAttribute("stroke-width")).toBe("1");
    expect(toSkip!.getAttribute("stroke-dasharray")).toBe("4 3");

    expect(toNone!.getAttribute("stroke")).toBe(colors.border);
    expect(toNone!.getAttribute("stroke-dasharray")).toBe("4 3");
    expect(toNone!.getAttribute("marker-end")).toBe("url(#wf-arrow)");

    // An unknown status falls back to the dim color and the pending look.
    expect(toOdd!.getAttribute("stroke")).toBe(colors.textDim);
    expect(toOdd!.getAttribute("opacity")).toBe("0.5");
    expect(nodeRect(container, "odd").getAttribute("fill")).toBe("#2a2a3a");
    expect(nodeEl(container, "odd").textContent).toContain("○");

    expect(toJoin!.getAttribute("stroke")).toBe("#ef4444");

    // A running node gets the pulsing outline.
    expect(nodeEl(container, "ok").querySelector("animate")).toBeTruthy();
    expect(nodeEl(container, "root").querySelector("animate")).toBeNull();

    // Type badges.
    expect(nodeEl(container, "ok").textContent).toContain("P");
    expect(nodeEl(container, "wait").textContent).toContain("A");
    expect(nodeEl(container, "skip").textContent).toContain("X");
  });

  it("handles cycles, missing deps and unknown node types", () => {
    const defs = [def("a", "bash", { depends_on: ["b"] }), def("b", "bash", { depends_on: ["a"] }), def("orphan", "custom" as WorkflowNodeDef["type"], { depends_on: ["ghost"] })];
    const { container } = render(<WorkflowGraph defs={defs} nodeRuns={[]} colors={colors} />);
    expect(nodeEl(container, "a")).toBeTruthy();
    expect(nodeEl(container, "b")).toBeTruthy();
    // The missing dep pushes orphan to layer 1 but draws no edge for it.
    expect(nodeRect(container, "orphan").getAttribute("x")).toBe("240");
    expect(nodeEl(container, "orphan").textContent).toContain("?");
    // Only the cycle's edges render: b→a and a→b.
    expect(edgePaths(container)).toHaveLength(2);
  });

  it("expands loop bodies into per-iteration nodes inside a labeled group", () => {
    const defs = [def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt", { depends_on: ["review"] })] }), def("one", "loop", { body: [def("solo", "bash")] })];
    const runs = [run("review", { iteration: 0 }), run("fix", { iteration: 0 }), run("review", { iteration: 1 }), run("fix", { iteration: 1, status: "failed" })];
    const { container } = render(<WorkflowGraph defs={defs} nodeRuns={runs} colors={colors} />);
    expect(screen.getByTestId("wf-loop-group-L").textContent).toBe("L · 2 iters");
    expect(screen.getByTestId("wf-loop-group-one").textContent).toBe("one · 1 iter");
    const fix1 = nodeEl(container, "L:iter1:fix");
    // Synthetic nodes show the body child's id and its iteration's run.
    expect(fix1.textContent).toContain("fix");
    expect(fix1.textContent).not.toContain("iter1");
    expect(nodeRect(container, "L:iter1:fix").getAttribute("fill")).toBe("#3a1a1a");
    // No run for solo yet, so it renders as pending.
    expect(nodeRect(container, "one:iter0:solo").getAttribute("fill")).toBe("#2a2a3a");
  });

  it("sizes a loop group around body children that run side by side", () => {
    const defs = [def("P", "loop", { body: [def("p", "bash"), def("q", "bash")] })];
    render(<WorkflowGraph defs={defs} nodeRuns={[]} colors={colors} />);
    // Both children sit in layer 0, stacked: 140 wide, 44 + 20 + 44 tall, padded.
    const rect = screen.getByTestId("wf-loop-group-P").querySelector("rect")!;
    expect(rect.getAttribute("width")).toBe(String(140 + 24));
    expect(rect.getAttribute("height")).toBe(String(108 + 40));
  });

  it("truncates long labels and shows retry and elapsed badges", () => {
    vi.useFakeTimers({ now: new Date("2026-01-01T02:00:00Z"), toFake: ["Date"] });
    const defs = [def("averyverylongnodeid", "bash"), def("mins", "bash"), def("hours", "bash"), def("live", "bash"), def("plain", "bash")];
    const runs = [
      run("averyverylongnodeid", { attempt: 3, started_at: "2026-01-01T00:00:00Z", finished_at: "2026-01-01T00:00:42Z" }),
      run("mins", { started_at: "2026-01-01T00:00:00Z", finished_at: "2026-01-01T00:05:10Z" }),
      run("hours", { started_at: "2026-01-01T00:00:00Z", finished_at: "2026-01-01T01:07:00Z" }),
      // Still running: elapsed runs to now.
      run("live", { status: "running", started_at: "2026-01-01T01:59:50Z" }),
      run("plain"),
    ];
    const { container } = render(<WorkflowGraph defs={defs} nodeRuns={runs} colors={colors} />);
    const long = nodeEl(container, "averyverylongnodeid");
    expect(long.textContent).toContain("averyverylo…");
    expect(long.textContent).toContain("R:2");
    expect(long.textContent).toContain("42s");
    expect(nodeEl(container, "mins").textContent).toContain("5m");
    expect(nodeEl(container, "hours").textContent).toContain("1h7m");
    expect(nodeEl(container, "live").textContent).toContain("10s");
    expect(nodeEl(container, "plain").textContent).not.toContain("R:");
    expect(nodeEl(container, "plain").textContent).toBe("✓plainB");
  });

  it("reports node clicks and highlights hovered and expanded nodes", async () => {
    const user = userEvent.setup();
    const onNodeClick = vi.fn();
    const defs = [def("a", "bash"), def("b", "bash")];
    const { container, rerender } = render(<WorkflowGraph defs={defs} nodeRuns={[]} colors={colors} onNodeClick={onNodeClick} expandedNodeId="b" />);

    await user.click(nodeEl(container, "a"));
    expect(onNodeClick).toHaveBeenCalledWith("a");

    await user.hover(nodeEl(container, "a"));
    expect(nodeRect(container, "a").getAttribute("stroke")).toBe(colors.textLight);
    expect(nodeRect(container, "a").getAttribute("stroke-width")).toBe("1.5");
    await user.unhover(nodeEl(container, "a"));
    expect(nodeRect(container, "a").getAttribute("stroke")).toBe("#555");
    expect(nodeRect(container, "a").getAttribute("stroke-width")).toBe("1");

    expect(nodeRect(container, "b").getAttribute("stroke")).toBe(colors.active);
    expect(nodeRect(container, "b").getAttribute("stroke-width")).toBe("2");
    // No run for the expanded node, so no output panel.
    expect(screen.queryByText("No output")).toBeNull();

    // Without a handler a click is a no-op.
    rerender(<WorkflowGraph defs={defs} nodeRuns={[]} colors={colors} />);
    await user.click(nodeEl(container, "a"));
    expect(onNodeClick).toHaveBeenCalledTimes(1);
  });

  describe("expanded node panel", () => {
    it("shows session, input, error and output of the expanded run", () => {
      const runs = [run("a", { status: "failed", session_id: "sess-1", input: "echo hi", error_text: "boom", output: "hi", started_at: "2026-01-01T00:00:00Z", finished_at: "2026-01-01T00:00:03Z" })];
      render(<WorkflowGraph defs={[def("a", "prompt")]} nodeRuns={runs} colors={colors} expandedNodeId="a" />);
      expect(screen.getByText("sess-1")).toBeTruthy();
      expect(screen.getByText("Input")).toBeTruthy();
      expect(screen.getByText("echo hi")).toBeTruthy();
      expect(screen.getByText("boom")).toBeTruthy();
      expect(screen.getByText("Output")).toBeTruthy();
      expect(screen.getByText("hi")).toBeTruthy();
      expect(screen.getByText("prompt")).toBeTruthy();
      // Elapsed shows in both the node and the panel header.
      expect(screen.getAllByText("3s")).toHaveLength(2);
      expect(screen.queryByText("No output")).toBeNull();
    });

    it("shows the output header for input alone and a placeholder when nothing was produced", () => {
      render(<WorkflowGraph defs={[def("a", "bash")]} nodeRuns={[run("a", { input: "ls" })]} colors={colors} expandedNodeId="a" />);
      expect(screen.getByText("Output")).toBeTruthy();
      expect(screen.getByText("No output")).toBeTruthy();
      expect(screen.queryByText("session")).toBeNull();
    });

    it("shows the error without an output placeholder", () => {
      render(<WorkflowGraph defs={[def("a", "bash")]} nodeRuns={[run("a", { status: "failed", error_text: "exit 1" })]} colors={colors} expandedNodeId="a" />);
      expect(screen.getByText("exit 1")).toBeTruthy();
      expect(screen.queryByText("Output")).toBeNull();
      expect(screen.queryByText("No output")).toBeNull();
    });

    it("labels a synthetic node with its iteration and falls back for unknown statuses", () => {
      const defs = [def("L", "loop", { body: [def("step", "bash")] })];
      const runs = [run("step", { iteration: 0 }), run("step", { iteration: 1, status: "weird" as WorkflowNodeRun["status"], output: "done" })];
      const { container } = render(<WorkflowGraph defs={defs} nodeRuns={runs} colors={colors} expandedNodeId="L:iter1:step" />);
      expect(screen.getByText("iter 2")).toBeTruthy();
      expect(screen.getByText("done")).toBeTruthy();
      const header = screen.getByText("iter 2").parentElement!;
      expect(header.textContent).toContain("step");
      const icon = header.firstElementChild as HTMLElement;
      expect(icon.textContent).toBe("○");
      expect(icon.style.color).toBe(cssColor(colors.textDim));
      // The minimap marks the unknown-status node in the pending gray.
      const marker = Array.from(minimap().children)[1] as HTMLElement;
      expect(marker.style.backgroundColor).toBe(cssColor("#555"));
      expect(container.querySelector('g[data-node="L"]')).toBeNull();
    });
  });

  describe("viewport", () => {
    function setup() {
      const utils = render(<WorkflowGraph defs={[def("a", "bash")]} nodeRuns={[]} colors={colors} />);
      return { ...utils, start: viewport(utils.container) };
    }

    it("zooms with the buttons around the canvas center and clamps the range", () => {
      const { container, start } = setup();
      fireEvent.click(screen.getByTitle("Zoom in"));
      let vp = viewport(container);
      expect(vp.zoom).toBeCloseTo(1.56);
      expect(vp.x).toBeCloseTo(300 - (300 - start.x) * 1.3);
      expect(screen.getByText("156%")).toBeTruthy();

      fireEvent.click(screen.getByText("156%"));
      expect(viewport(container).zoom).toBe(1);

      fireEvent.click(screen.getByTitle("Zoom out"));
      expect(viewport(container).zoom).toBeCloseTo(1 / 1.3);

      for (let i = 0; i < 10; i++) fireEvent.click(screen.getByTitle("Zoom out"));
      expect(viewport(container).zoom).toBe(0.25);
      for (let i = 0; i < 12; i++) fireEvent.click(screen.getByTitle("Zoom in"));
      vp = viewport(container);
      expect(vp.zoom).toBe(3);
      expect(screen.getByText("300%")).toBeTruthy();
    });

    it("highlights the zoom buttons on hover", () => {
      setup();
      for (const title of ["Zoom in", "Zoom out"]) {
        const btn = screen.getByTitle(title);
        fireEvent.mouseEnter(btn);
        expect(btn.style.color).toBe(cssColor(colors.textLight));
        fireEvent.mouseLeave(btn);
        expect(btn.style.color).toBe(cssColor(colors.textDim));
      }
    });

    it("pans with the wheel and zooms toward the cursor with ctrl or meta", () => {
      const { container, start } = setup();
      const el = canvas(container);
      act(() => {
        el.dispatchEvent(new WheelEvent("wheel", { deltaX: 10, deltaY: 20, bubbles: true, cancelable: true }));
      });
      let vp = viewport(container);
      expect(vp.x).toBeCloseTo(start.x - 10);
      expect(vp.y).toBeCloseTo(start.y - 20);
      expect(vp.zoom).toBeCloseTo(start.zoom);

      const zoomEvent = new WheelEvent("wheel", { deltaY: -100, ctrlKey: true, clientX: 100, clientY: 50, bubbles: true, cancelable: true });
      act(() => {
        el.dispatchEvent(zoomEvent);
      });
      expect(zoomEvent.defaultPrevented).toBe(true);
      const before = vp;
      vp = viewport(container);
      expect(vp.zoom).toBeCloseTo(before.zoom * 1.3);
      expect(vp.x).toBeCloseTo(100 - (100 - before.x) * 1.3);
      expect(vp.y).toBeCloseTo(50 - (50 - before.y) * 1.3);

      act(() => {
        el.dispatchEvent(new WheelEvent("wheel", { deltaY: 100, metaKey: true, bubbles: true, cancelable: true }));
      });
      expect(viewport(container).zoom).toBeCloseTo(vp.zoom * 0.7);
    });

    it("does not attach the wheel handler when the first render had no nodes", () => {
      const { container, rerender } = render(<WorkflowGraph defs={[]} nodeRuns={[]} colors={colors} />);
      rerender(<WorkflowGraph defs={[def("a", "bash")]} nodeRuns={[]} colors={colors} />);
      const start = viewport(container);
      act(() => {
        canvas(container).dispatchEvent(new WheelEvent("wheel", { deltaX: 10, bubbles: true }));
      });
      expect(viewport(container)).toEqual(start);
    });

    it("pans by dragging the background until the mouse is released", () => {
      const { container, start } = setup();
      const el = canvas(container);
      fireEvent.mouseDown(el, { button: 0, clientX: 100, clientY: 100 });
      fireEvent.mouseMove(document, { clientX: 150, clientY: 120 });
      let vp = viewport(container);
      expect(vp.x).toBeCloseTo(start.x + 50);
      expect(vp.y).toBeCloseTo(start.y + 20);
      expect(el.style.cursor).toBe("grabbing");

      fireEvent.mouseUp(document);
      fireEvent.mouseMove(document, { clientX: 300, clientY: 300 });
      expect(viewport(container)).toEqual(vp);

      // Other buttons and presses on a node don't start a pan.
      fireEvent.mouseDown(el, { button: 2, clientX: 0, clientY: 0 });
      fireEvent.mouseMove(document, { clientX: 50, clientY: 50 });
      expect(viewport(container)).toEqual(vp);
      fireEvent.mouseDown(nodeRect(container, "a"), { button: 0, clientX: 0, clientY: 0 });
      fireEvent.mouseMove(document, { clientX: 50, clientY: 50 });
      vp = viewport(container);
      expect(vp.x).toBeCloseTo(start.x + 50);
      fireEvent.mouseUp(document);
    });

    it("recenters on a minimap click and drags the minimap viewport rect", () => {
      const { container, start } = setup();
      // Rerender so the minimap measures the mounted canvas.
      fireEvent.click(screen.getByText("120%"));
      fireEvent.click(screen.getByTitle("Zoom in"));
      fireEvent.click(screen.getByTitle("Zoom out"));
      const vp0 = viewport(container);
      expect(vp0.zoom).toBeCloseTo(1);

      // At zoom 1 the minimap spans the 220x124 layout plus the 600x400 view
      // at -x/-y, scaled to fit 120x80.
      const world = (v: { x: number; y: number }) => {
        const minX = Math.min(0, -v.x);
        const minY = Math.min(0, -v.y);
        const w = Math.max(220, -v.x + 600) - minX;
        const h = Math.max(124, -v.y + 400) - minY;
        return { minX, minY, scale: Math.min(120 / w, 80 / h) };
      };
      const { minX: worldMinX, minY: worldMinY } = world(vp0);

      fireEvent.click(minimap(), { clientX: 0, clientY: 0 });
      const vp = viewport(container);
      expect(vp.x).toBeCloseTo(-(worldMinX - 300));
      expect(vp.y).toBeCloseTo(-(worldMinY - 200));

      const rectEl = minimap().lastElementChild as HTMLElement;
      // Clicks on the viewport rect don't recenter.
      fireEvent.click(rectEl, { clientX: 50, clientY: 50 });
      expect(viewport(container)).toEqual(vp);

      const { scale } = world(vp);
      fireEvent.mouseDown(rectEl, { clientX: 10, clientY: 10 });
      fireEvent.mouseMove(document, { clientX: 20, clientY: 15 });
      const dragged = viewport(container);
      expect(dragged.x).toBeCloseTo(vp.x - 10 / scale);
      expect(dragged.y).toBeCloseTo(vp.y - 5 / scale);
      fireEvent.mouseUp(document);
      fireEvent.mouseMove(document, { clientX: 90, clientY: 90 });
      expect(viewport(container)).toEqual(dragged);
      expect(start.zoom).toBeCloseTo(1.2);
    });
  });
});
