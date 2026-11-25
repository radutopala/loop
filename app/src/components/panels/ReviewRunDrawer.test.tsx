import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchWorkflowRun, type WorkflowNodeDef, type WorkflowNodeRun } from "../../api/workflows";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { darkColors } from "../../theme";
import type { WSEvent } from "../../types";
import { ReviewRunDrawer } from "./ReviewRunDrawer";

vi.mock("../../api/workflows", () => ({
  fetchWorkflowRun: vi.fn(),
}));

// WorkflowGraph lays out an SVG canvas; the stub lists what the drawer hands
// it and exposes onNodeClick.
vi.mock("./WorkflowGraph", () => ({
  WorkflowGraph: ({ defs, nodeRuns, expandedNodeId, onNodeClick }: { defs: WorkflowNodeDef[]; nodeRuns: WorkflowNodeRun[]; expandedNodeId: string | null; onNodeClick: (id: string) => void }) => (
    <div data-testid="graph">
      <span data-testid="graph-defs">{defs.map((d) => d.id).join(",")}</span>
      <span data-testid="graph-runs">{nodeRuns.length}</span>
      <span data-testid="graph-expanded">{expandedNodeId ?? "none"}</span>
      <button onClick={() => onNodeClick("n1")}>click n1</button>
    </div>
  ),
}));

const fetchMock = vi.mocked(fetchWorkflowRun);

const detail = (over: { status?: string; def?: string; name?: string; noRun?: boolean; noNodes?: boolean } = {}) =>
  ({
    run: over.noRun ? undefined : { id: "r1", workflow_name: over.name, status: over.status ?? "completed", workflow_def: over.def },
    node_runs: over.noNodes ? undefined : [{ id: "nr1" }],
  }) as never;

// A subscription that records the drawer's listener.
function subscriptions() {
  const listeners: ChatEventListener[] = [];
  const unsubscribe = vi.fn();
  const subscribe = vi.fn((l: ChatEventListener) => {
    listeners.push(l);
    return unsubscribe;
  });
  const emit = (event: Partial<WSEvent>) => act(() => listeners.forEach((l) => l(event as WSEvent)));
  return { subscribe, unsubscribe, emit };
}

describe("ReviewRunDrawer", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    fetchMock.mockResolvedValue(detail({ def: JSON.stringify({ nodes: [{ id: "a" }, { id: "b" }] }), name: "review-loop" }));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("fetches the run and renders its canvas with the workflow name", async () => {
    const onToggle = vi.fn();
    render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={onToggle} />);
    expect(await screen.findByText("review-loop")).toBeTruthy();
    expect(screen.getByTestId("graph-defs").textContent).toBe("a,b");
    expect(screen.getByTestId("graph-runs").textContent).toBe("1");
    expect(fetchMock).toHaveBeenCalledWith("r1", { signal: expect.any(AbortSignal) });
    const toggle = screen.getByTestId("review-run-drawer-toggle");
    expect(toggle.title).toBe("Hide workflow canvas");
    await userEvent.click(toggle);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("hides the canvas when collapsed", async () => {
    render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed onToggleCollapsed={vi.fn()} />);
    expect(await screen.findByText("review-loop")).toBeTruthy();
    expect(screen.queryByTestId("graph")).toBeNull();
    expect(screen.getByTestId("review-run-drawer-toggle").title).toBe("Show workflow canvas");
  });

  it("falls back to empty defs, runs and name for sparse or bad details", async () => {
    fetchMock.mockResolvedValue(detail({ noRun: true, noNodes: true }));
    const { rerender } = render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(screen.getByTestId("graph-defs").textContent).toBe("");
    expect(screen.getByTestId("graph-runs").textContent).toBe("0");
    expect(screen.queryByText("review-loop")).toBeNull();

    // Unparseable def, and a def without nodes.
    fetchMock.mockResolvedValue(detail({ def: "{not json", name: "w" }));
    rerender(<ReviewRunDrawer runId="r2" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    expect(await screen.findByText("w")).toBeTruthy();
    expect(screen.getByTestId("graph-defs").textContent).toBe("");
    fetchMock.mockResolvedValue(detail({ def: "{}", name: "w2" }));
    rerender(<ReviewRunDrawer runId="r3" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    expect(await screen.findByText("w2")).toBeTruthy();
    expect(screen.getByTestId("graph-defs").textContent).toBe("");
  });

  it("keeps the last canvas when a fetch fails", async () => {
    const subs = subscriptions();
    render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} subscribeChatEvents={subs.subscribe} />);
    expect(await screen.findByText("review-loop")).toBeTruthy();
    fetchMock.mockRejectedValue(new Error("offline"));
    await subs.emit({ type: "workflow.node_started", data: { run_id: "r1" } });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(screen.getByText("review-loop")).toBeTruthy();
    expect(screen.getByTestId("graph-defs").textContent).toBe("a,b");
  });

  it("toggles the expanded node and resets it for a new run", async () => {
    const { rerender } = render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    await screen.findByText("review-loop");
    await userEvent.click(screen.getByText("click n1"));
    expect(screen.getByTestId("graph-expanded").textContent).toBe("n1");
    await userEvent.click(screen.getByText("click n1"));
    expect(screen.getByTestId("graph-expanded").textContent).toBe("none");
    await userEvent.click(screen.getByText("click n1"));
    rerender(<ReviewRunDrawer runId="r2" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    expect(screen.getByTestId("graph-expanded").textContent).toBe("none");
  });

  it("refetches on this run's workflow events while expanded only", async () => {
    const subs = subscriptions();
    const { rerender, unmount } = render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} subscribeChatEvents={subs.subscribe} />);
    await screen.findByText("review-loop");
    // Mount fetch plus the expanded-state fetch.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const type of ["workflow.run_started", "workflow.run_completed", "workflow.run_paused", "workflow.node_started", "workflow.node_completed"]) {
      await subs.emit({ type: type as WSEvent["type"], data: { run_id: "r1" } });
    }
    expect(fetchMock).toHaveBeenCalledTimes(7);
    expect(fetchMock).toHaveBeenLastCalledWith("r1", undefined);
    // Other runs, missing data and other event types are ignored.
    await subs.emit({ type: "workflow.node_started", data: { run_id: "other" } });
    await subs.emit({ type: "workflow.node_started" });
    await subs.emit({ type: "message.created" as WSEvent["type"], data: { run_id: "r1" } });
    expect(fetchMock).toHaveBeenCalledTimes(7);

    rerender(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed onToggleCollapsed={vi.fn()} subscribeChatEvents={subs.subscribe} />);
    await subs.emit({ type: "workflow.node_completed", data: { run_id: "r1" } });
    expect(fetchMock).toHaveBeenCalledTimes(7);
    // Expanding again pulls the latest state.
    rerender(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} subscribeChatEvents={subs.subscribe} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(8));
    unmount();
    expect(subs.unsubscribe).toHaveBeenCalled();
  });

  it("polls every 2s while the run is active and the drawer is open", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fetchMock.mockResolvedValue(detail({ status: "running", name: "loop" }));
    const { rerender } = render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    await act(async () => {});
    const before = fetchMock.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(2_000);
    });
    expect(fetchMock.mock.calls.length).toBe(before + 1);

    // Paused also counts as active.
    fetchMock.mockResolvedValue(detail({ status: "paused", name: "loop" }));
    await act(async () => {
      vi.advanceTimersByTime(2_000);
    });
    await act(async () => {
      vi.advanceTimersByTime(2_000);
    });
    expect(fetchMock.mock.calls.length).toBe(before + 3);

    // Collapsing stops polling.
    rerender(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed onToggleCollapsed={vi.fn()} />);
    const collapsedCount = fetchMock.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(6_000);
    });
    expect(fetchMock.mock.calls.length).toBe(collapsedCount);
  });

  it("does not poll a finished run", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    render(<ReviewRunDrawer runId="r1" colors={darkColors} collapsed={false} onToggleCollapsed={vi.fn()} />);
    await act(async () => {});
    const before = fetchMock.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(6_000);
    });
    expect(fetchMock.mock.calls.length).toBe(before);
  });
});
