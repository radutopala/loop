import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../ThemeContext";
import { SplitDivider, SplitPaneLayout } from "./SplitPaneLayout";
import type { LeafNode, PaneNode, SplitNode } from "./types";

const leaf = (id: string, flex: number): LeafNode => ({ type: "leaf", id, panel: "chat", flex });
const split = (direction: SplitNode["direction"], children: PaneNode[]): SplitNode => ({ type: "split", direction, children, flex: 1 });

type Props = Partial<Parameters<typeof SplitPaneLayout>[0]>;

/** The layout over a tree it keeps in state, applying the flex updates of the
 *  root split the way the workspace does. */
function Harness({ initial, onUpdateFlex, ...props }: { initial: PaneNode } & Props) {
  const [tree, setTree] = useState(initial);
  return (
    <SplitPaneLayout
      tree={tree}
      renderLeaf={(l) => <div data-testid={`content-${l.id}`}>{l.id}</div>}
      onUpdateFlex={(path, idx, a, b) => {
        onUpdateFlex?.(path, idx, a, b);
        setTree((t) => {
          if (t.type !== "split" || path.length) return t;
          const children = t.children.map((c, i) => (i === idx ? { ...c, flex: a } : i === idx + 1 ? { ...c, flex: b } : c));
          return { ...t, children };
        });
      }}
      onDrop={vi.fn()}
      onRemoveLeaf={vi.fn()}
      onSplitLeaf={vi.fn()}
      {...props}
    />
  );
}

/** The divider with the given resize cursor; its parent is the split's box,
 *  given a size since jsdom has no layout. */
function divider(cursor: "col-resize" | "row-resize", size: { width: number; height: number }) {
  const el = document.querySelector<HTMLElement>(`[style*="cursor: ${cursor}"]`);
  if (!el) throw new Error(`no ${cursor} divider`);
  vi.spyOn(el.parentElement!, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, ...size } as DOMRect);
  return el;
}

const paneFlex = (id: string) => screen.getByTestId(`content-${id}`).parentElement!.parentElement!.style.flex;

describe("SplitPaneLayout", () => {
  it("renders every leaf with a divider between siblings", () => {
    render(<Harness initial={split("horizontal", [leaf("a", 1), leaf("b", 1), leaf("c", 1)])} />);
    expect(screen.getByTestId("content-a")).toBeTruthy();
    expect(screen.getByTestId("content-b")).toBeTruthy();
    expect(screen.getByTestId("content-c")).toBeTruthy();
    expect(document.querySelectorAll('[style*="cursor: col-resize"]')).toHaveLength(2);
  });

  it("resizes a horizontal split by the dragged distance and shows each side's share", () => {
    const onUpdateFlex = vi.fn();
    render(<Harness initial={split("horizontal", [leaf("a", 1), leaf("b", 1)])} onUpdateFlex={onUpdateFlex} />);
    const bar = divider("col-resize", { width: 1000, height: 500 });

    fireEvent.mouseDown(bar, { clientX: 500 });
    expect(document.body.style.cursor).toBe("col-resize");
    expect(document.body.style.userSelect).toBe("none");
    fireEvent.mouseMove(document, { clientX: 600 });

    const [path, idx, a, b] = onUpdateFlex.mock.calls.at(-1)!;
    expect(path).toEqual([]);
    expect(idx).toBe(0);
    expect(a).toBeCloseTo(1.2);
    expect(b).toBeCloseTo(0.8);
    expect(screen.getAllByTestId("pane-resize-percent").map((el) => el.textContent)).toEqual(["60%", "40%"]);

    fireEvent.mouseUp(document);
    expect(screen.queryByTestId("pane-resize-percent")).toBeNull();
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");
    onUpdateFlex.mockClear();
    fireEvent.mouseMove(document, { clientX: 900 });
    expect(onUpdateFlex).not.toHaveBeenCalled();
  });

  it.each([
    { to: 1000, wantA: 1.85 },
    { to: 0, wantA: 0.15 },
  ])("keeps a pane at least 0.15 wide when dragged to $to", ({ to, wantA }) => {
    const onUpdateFlex = vi.fn();
    render(<Harness initial={split("horizontal", [leaf("a", 1), leaf("b", 1)])} onUpdateFlex={onUpdateFlex} />);
    fireEvent.mouseDown(divider("col-resize", { width: 1000, height: 500 }), { clientX: 500 });
    fireEvent.mouseMove(document, { clientX: to });
    fireEvent.mouseUp(document);
    const [, , a, b] = onUpdateFlex.mock.calls.at(-1)!;
    expect(a).toBeCloseTo(wantA);
    expect(b).toBeCloseTo(2 - wantA);
  });

  it("resizes a vertical split along its height", () => {
    const onUpdateFlex = vi.fn();
    render(<Harness initial={split("vertical", [leaf("a", 1), leaf("b", 1)])} onUpdateFlex={onUpdateFlex} />);
    const bar = divider("row-resize", { width: 1000, height: 400 });
    fireEvent.mouseDown(bar, { clientY: 200, clientX: 0 });
    expect(document.body.style.cursor).toBe("row-resize");
    fireEvent.mouseMove(document, { clientY: 100, clientX: 900 });
    fireEvent.mouseUp(document);
    const [, , a, b] = onUpdateFlex.mock.calls.at(-1)!;
    expect(a).toBeCloseTo(0.5);
    expect(b).toBeCloseTo(1.5);
  });

  it("keeps frames from taking the mouse while a divider is dragged", () => {
    render(<Harness initial={split("horizontal", [leaf("a", 1), leaf("b", 1)])} renderLeaf={(l) => <iframe title={l.id} />} />);
    const frames = screen.getAllByTitle(/^[ab]$/) as HTMLIFrameElement[];
    fireEvent.mouseDown(divider("col-resize", { width: 1000, height: 500 }), { clientX: 500 });
    expect(frames.map((f) => f.style.pointerEvents)).toEqual(["none", "none"]);
    fireEvent.mouseUp(document);
    expect(frames.map((f) => f.style.pointerEvents)).toEqual(["", ""]);
  });

  it("collapses a minimized pane and gives its share to the rest", () => {
    render(<Harness initial={split("horizontal", [leaf("a", 0.5), leaf("b", 0.25), leaf("c", 0.25)])} minimizedLeaves={new Set(["a"])} />);
    expect(screen.queryByTestId("content-a")).toBeNull();
    // b and c add up to 0.5 of the row, so both double to fill it.
    expect(paneFlex("b")).toBe("0.5 1 0%");
    expect(paneFlex("c")).toBe("0.5 1 0%");
  });

  it("wires the header's close, minimize and expand buttons to the leaf", async () => {
    const onRemoveLeaf = vi.fn();
    const onToggleMinimize = vi.fn();
    const onMaximize = vi.fn();
    const user = userEvent.setup();
    render(<Harness initial={leaf("only", 1)} onRemoveLeaf={onRemoveLeaf} onToggleMinimize={onToggleMinimize} onMaximize={onMaximize} />);
    await user.click(screen.getByTitle("Close pane"));
    await user.click(screen.getByTitle("Minimize pane"));
    await user.click(screen.getByTitle("Expand pane"));
    expect(onRemoveLeaf).toHaveBeenCalledWith("only");
    expect(onToggleMinimize).toHaveBeenCalledWith("only");
    expect(onMaximize).toHaveBeenCalledWith("only");
  });

  it("collapses a fully minimized split inside a column but not inside a row", () => {
    const minimized = new Set(["a", "b"]);
    const { unmount } = render(<Harness initial={split("vertical", [split("horizontal", [leaf("a", 1), leaf("b", 1)]), leaf("c", 1)])} minimizedLeaves={minimized} />);
    // The root split is the box around c's pane; the nested split comes first in it.
    const firstOfRoot = () => screen.getByTestId("content-c").parentElement!.parentElement!.parentElement!.firstElementChild as HTMLElement;
    expect(firstOfRoot().style.flex).toBe("0 0 auto");
    unmount();
    render(<Harness initial={split("horizontal", [split("vertical", [leaf("a", 1), leaf("b", 1)]), leaf("c", 1)])} minimizedLeaves={minimized} />);
    expect(firstOfRoot().style.flex).toBe("1 1 0%");
  });

  it("scales a nested split's flex when minimized siblings leave less than a full share", () => {
    render(<Harness initial={split("horizontal", [leaf("a", 0.5), { ...split("vertical", [leaf("b", 1), leaf("c", 1)]), flex: 0.25 }])} minimizedLeaves={new Set(["a"])} />);
    // b's pane sits in the nested split, which doubles from 0.25 to fill the row.
    const nested = screen.getByTestId("content-b").parentElement!.parentElement!.parentElement!;
    expect(nested.style.flexDirection).toBe("column");
    expect(nested.style.flex).toBe("1 1 0%");
  });

  it("shows a nested split's share while dragging the divider beside it", () => {
    render(<Harness initial={split("horizontal", [leaf("a", 1), split("vertical", [leaf("b", 1), leaf("c", 1)])])} />);
    const bar = divider("col-resize", { width: 1000, height: 500 });
    fireEvent.mouseDown(bar, { clientX: 500 });
    expect(screen.getAllByTestId("pane-resize-percent").map((el) => el.textContent)).toEqual(["50%", "50%"]);
    fireEvent.mouseUp(document);
  });

  it("passes a docker agent pane its agent's info", () => {
    const agents = new Map([["ag", { agent_id: "x", channel_id: "c", name: "Builder", status: "running", work_summary: "" }]]);
    render(<Harness initial={split("horizontal", [{ ...leaf("ag", 1), panel: "docker-agent" }, leaf("a", 1)])} agentInfoMap={agents} />);
    expect(screen.getByText("Builder")).toBeTruthy();
    expect(screen.getByTitle("running")).toBeTruthy();
  });
});

describe("SplitDivider", () => {
  const bar = (container: HTMLElement) => container.firstElementChild as HTMLElement;

  it("is a plain gap between island panes", () => {
    const { container } = render(<SplitDivider vertical={false} onMouseDown={vi.fn()} />);
    expect(bar(container).style.width).toBe("6px");
    expect(bar(container).style.backgroundColor).toBe("transparent");
    expect(bar(container).childElementCount).toBe(0);
  });

  it.each([
    { vertical: true, size: "height", cursor: "row-resize", gripWidth: "24px", gripHeight: "2px" },
    { vertical: false, size: "width", cursor: "col-resize", gripWidth: "2px", gripHeight: "24px" },
  ] as const)("draws a grip on a resizable bar without islands (vertical $vertical)", ({ vertical, size, cursor, gripWidth, gripHeight }) => {
    const { container } = render(
      <ThemeProvider initialIslands={false}>
        <SplitDivider vertical={vertical} onMouseDown={vi.fn()} />
      </ThemeProvider>,
    );
    const el = bar(container);
    expect(el.style[size]).toBe("4px");
    expect(el.style.cursor).toBe(cursor);
    expect(el.style.backgroundColor).not.toBe("transparent");
    const grip = el.firstElementChild as HTMLElement;
    expect(grip.style.width).toBe(gripWidth);
    expect(grip.style.height).toBe(gripHeight);
  });

  it("has no grip or resize cursor without a handler", () => {
    const { container } = render(
      <ThemeProvider initialIslands={false}>
        <SplitDivider vertical />
      </ThemeProvider>,
    );
    expect(bar(container).style.cursor).toBe("");
    expect(bar(container).childElementCount).toBe(0);
  });
});
